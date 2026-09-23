package kimioauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	ClientID        = "17e5f671-d194-4dfb-9706-5516cb48c098"
	DeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	OAuthBaseURL    = "https://auth.kimi.com"
	APIBaseURL      = "https://api.kimi.com/coding"

	defaultPollInterval    = 5 * time.Second
	defaultSlowDown        = 5 * time.Second
	defaultMaxPollDuration = 15 * time.Minute
	maxResponseBody        = 1 << 20
)

type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func (d DeviceCode) VerificationURL() string {
	if strings.TrimSpace(d.VerificationURIComplete) != "" {
		return d.VerificationURIComplete
	}
	return d.VerificationURI
}

type Token struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	ExpiresAt    time.Time
}

type Client struct {
	HTTPClient        *http.Client
	OAuthBaseURL      string
	DeviceID          string
	Version           string
	MinPollInterval   time.Duration
	SlowDownIncrement time.Duration
	MaxPollDuration   time.Duration
	Now               func() time.Time
}

func NewClient(version string) *Client {
	return &Client{
		HTTPClient:        &http.Client{Timeout: 30 * time.Second},
		OAuthBaseURL:      OAuthBaseURL,
		DeviceID:          newDeviceID(),
		Version:           version,
		MinPollInterval:   defaultPollInterval,
		SlowDownIncrement: defaultSlowDown,
		MaxPollDuration:   defaultMaxPollDuration,
	}
}

func (c *Client) DeviceIdentifier() string {
	if strings.TrimSpace(c.DeviceID) == "" {
		c.DeviceID = newDeviceID()
	}
	return c.DeviceID
}

func (c *Client) RequestDeviceCode(ctx context.Context) (DeviceCode, error) {
	form := url.Values{"client_id": {ClientID}}
	req, err := c.newRequest(ctx, "/api/oauth/device_authorization", form)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("create Kimi device authorization request: %w", err)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return DeviceCode{}, fmt.Errorf("request Kimi device authorization: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		return DeviceCode{}, fmt.Errorf("Kimi device authorization returned HTTP %d", resp.StatusCode)
	}
	var device DeviceCode
	if err := decodeJSON(resp.Body, &device); err != nil {
		return DeviceCode{}, fmt.Errorf("parse Kimi device authorization response: %w", err)
	}
	if strings.TrimSpace(device.DeviceCode) == "" || strings.TrimSpace(device.VerificationURL()) == "" {
		return DeviceCode{}, errors.New("Kimi device authorization response is incomplete")
	}
	return device, nil
}

func (c *Client) PollForToken(ctx context.Context, device DeviceCode) (Token, error) {
	if strings.TrimSpace(device.DeviceCode) == "" {
		return Token{}, errors.New("Kimi device code is required")
	}
	interval := time.Duration(device.Interval) * time.Second
	minimum := c.MinPollInterval
	if minimum <= 0 {
		minimum = defaultPollInterval
	}
	if interval < minimum {
		interval = minimum
	}
	maxWait := c.MaxPollDuration
	if maxWait <= 0 {
		maxWait = defaultMaxPollDuration
	}
	if device.ExpiresIn > 0 && time.Duration(device.ExpiresIn)*time.Second < maxWait {
		maxWait = time.Duration(device.ExpiresIn) * time.Second
	}
	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()

	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return Token{}, fmt.Errorf("Kimi authorization cancelled: %w", ctx.Err())
		case <-deadline.C:
			stopTimer(timer)
			return Token{}, errors.New("Kimi device code expired")
		case <-timer.C:
		}

		token, oauthError, err := c.exchangeDeviceCode(ctx, device.DeviceCode)
		if err != nil {
			return Token{}, err
		}
		if token.AccessToken != "" {
			return token, nil
		}
		switch oauthError {
		case "authorization_pending":
			continue
		case "slow_down":
			increment := c.SlowDownIncrement
			if increment <= 0 {
				increment = defaultSlowDown
			}
			interval += increment
		case "expired_token":
			return Token{}, errors.New("Kimi device code expired")
		case "access_denied":
			return Token{}, errors.New("Kimi authorization access denied by user")
		default:
			return Token{}, fmt.Errorf("Kimi OAuth error: %s", safeOAuthError(oauthError))
		}
	}
}

func (c *Client) exchangeDeviceCode(ctx context.Context, deviceCode string) (Token, string, error) {
	form := url.Values{
		"client_id":   {ClientID},
		"device_code": {deviceCode},
		"grant_type":  {DeviceGrantType},
	}
	req, err := c.newRequest(ctx, "/api/oauth/token", form)
	if err != nil {
		return Token{}, "", fmt.Errorf("create Kimi token request: %w", err)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Token{}, "", fmt.Errorf("request Kimi token: %w", err)
	}
	defer resp.Body.Close()
	var envelope struct {
		Error        string  `json:"error"`
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		TokenType    string  `json:"token_type"`
		Scope        string  `json:"scope"`
		ExpiresIn    float64 `json:"expires_in"`
	}
	if err := decodeJSON(resp.Body, &envelope); err != nil {
		return Token{}, "", fmt.Errorf("parse Kimi token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if envelope.Error != "" {
		return Token{}, envelope.Error, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Token{}, "", fmt.Errorf("Kimi token endpoint returned HTTP %d", resp.StatusCode)
	}
	if strings.TrimSpace(envelope.AccessToken) == "" {
		return Token{}, "", errors.New("Kimi token response contained no access token")
	}
	tokenType := strings.TrimSpace(envelope.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	token := Token{
		AccessToken:  envelope.AccessToken,
		RefreshToken: envelope.RefreshToken,
		TokenType:    tokenType,
		Scope:        envelope.Scope,
	}
	if envelope.ExpiresIn > 0 {
		token.ExpiresAt = c.now().Add(time.Duration(envelope.ExpiresIn * float64(time.Second)))
	}
	return token, "", nil
}

func (c *Client) newRequest(ctx context.Context, path string, form url.Values) (*http.Request, error) {
	base := strings.TrimRight(strings.TrimSpace(c.OAuthBaseURL), "/")
	if base == "" {
		base = OAuthBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Msh-Platform", "codex-cliproxy-gateway")
	version := strings.TrimSpace(c.Version)
	if version == "" {
		version = "unknown"
	}
	req.Header.Set("X-Msh-Version", version)
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unknown"
	}
	req.Header.Set("X-Msh-Device-Name", hostname)
	req.Header.Set("X-Msh-Device-Model", runtime.GOOS+" "+runtime.GOARCH)
	deviceID := strings.TrimSpace(c.DeviceID)
	if deviceID == "" {
		deviceID = newDeviceID()
		c.DeviceID = deviceID
	}
	req.Header.Set("X-Msh-Device-Id", deviceID)
	return req, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func decodeJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxResponseBody))
	return decoder.Decode(target)
}

func newDeviceID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("gateway-%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func safeOAuthError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown_error"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return "unknown_error"
		}
	}
	if len(value) > 80 {
		return "unknown_error"
	}
	return value
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
