package kimioauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceFlowRequestsAndPollsWithoutLeakingTokens(t *testing.T) {
	const accessToken = "secret-access-token"
	const refreshToken = "secret-refresh-token"
	var polls atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		for _, header := range []string{"X-Msh-Platform", "X-Msh-Version", "X-Msh-Device-Name", "X-Msh-Device-Model", "X-Msh-Device-Id"} {
			if strings.TrimSpace(r.Header.Get(header)) == "" {
				t.Errorf("missing %s header", header)
			}
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("client_id"); got != ClientID {
			t.Errorf("client_id = %q", got)
		}
		switch r.URL.Path {
		case "/api/oauth/device_authorization":
			return jsonResponse(t, http.StatusOK, map[string]any{
				"device_code": "device-secret", "user_code": "ABCD-EFGH",
				"verification_uri":          "https://auth.test/activate",
				"verification_uri_complete": "https://auth.test/activate?code=ABCD-EFGH",
				"expires_in":                60, "interval": 0,
			}), nil
		case "/api/oauth/token":
			if r.Form.Get("device_code") != "device-secret" || r.Form.Get("grant_type") != DeviceGrantType {
				t.Errorf("unexpected token form: %v", r.Form)
			}
			if polls.Add(1) == 1 {
				return jsonResponse(t, http.StatusOK, map[string]any{"error": "authorization_pending"}), nil
			}
			return jsonResponse(t, http.StatusOK, map[string]any{
				"access_token": accessToken, "refresh_token": refreshToken,
				"token_type": "Bearer", "scope": "coding", "expires_in": 3600,
			}), nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return nil, nil
		}
	})

	client := testClient(transport)
	device, err := client.RequestDeviceCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if device.UserCode != "ABCD-EFGH" || !strings.Contains(device.VerificationURIComplete, "ABCD-EFGH") {
		t.Fatalf("device response = %#v", device)
	}
	token, err := client.PollForToken(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != accessToken || token.RefreshToken != refreshToken || token.ExpiresAt.IsZero() {
		t.Fatalf("token response was not preserved")
	}
	if polls.Load() != 2 {
		t.Fatalf("polls = %d", polls.Load())
	}
}

func TestDeviceFlowHandlesSlowDownAndTerminalErrors(t *testing.T) {
	tests := []struct {
		name      string
		responses []string
		want      string
	}{
		{"slow down then success", []string{`{"error":"slow_down"}`, `{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":60}`}, ""},
		{"expired", []string{`{"error":"expired_token"}`}, "device code expired"},
		{"denied", []string{`{"error":"access_denied"}`}, "access denied"},
		{"unknown redacted", []string{`{"error":"server_error","error_description":"secret-response-value"}`}, "OAuth error: server_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var index atomic.Int32
			client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				i := int(index.Add(1) - 1)
				if i >= len(test.responses) {
					i = len(test.responses) - 1
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.responses[i]))}, nil
			}))
			_, err := client.PollForToken(context.Background(), DeviceCode{DeviceCode: "device-secret", ExpiresIn: 60})
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "secret-response-value") || strings.Contains(err.Error(), "device-secret") {
				t.Fatalf("secret leaked in error: %v", err)
			}
		})
	}
}

func TestDeviceFlowHonorsContextCancellation(t *testing.T) {
	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(t, http.StatusOK, map[string]any{"error": "authorization_pending"}), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.PollForToken(ctx, DeviceCode{DeviceCode: "device-secret", ExpiresIn: 60})
	if err == nil || !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("error = %v", err)
	}
}

func TestSaveCredentialMatchesCLIProxyAPIFormatAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth")
	now := time.Date(2026, 9, 23, 12, 34, 56, 789_000_000, time.UTC)
	path, err := SaveCredential(dir, Token{
		AccessToken: "secret-access-token", RefreshToken: "secret-refresh-token",
		TokenType: "Bearer", Scope: "coding", ExpiresAt: now.Add(time.Hour),
	}, "device-id", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || filepath.Base(path) != fmt.Sprintf("kimi-%d.json", now.UnixMilli()) {
		t.Fatalf("path = %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var credential map[string]any
	if err := json.Unmarshal(data, &credential); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"access_token": "secret-access-token", "refresh_token": "secret-refresh-token", "token_type": "Bearer",
		"scope": "coding", "device_id": "device-id", "expired": now.Add(time.Hour).Format(time.RFC3339),
		"type": "kimi", "domain": "kimi.com", "base_url": APIBaseURL,
		"timestamp": float64(now.UnixMilli()), "disabled": false, "priority": float64(100),
	}
	for key, value := range want {
		if fmt.Sprint(credential[key]) != fmt.Sprint(value) {
			t.Errorf("%s = %#v, want %#v", key, credential[key], value)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential mode = %o", info.Mode().Perm())
		}
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("auth dir mode = %o", dirInfo.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("auth dir contains %d entries", len(entries))
	}
}

func TestEnsureCredentialPriorityMigratesOnlyKimiOAuthFiles(t *testing.T) {
	dir := t.TempDir()
	kimiPath := filepath.Join(dir, "kimi-existing.json")
	otherPath := filepath.Join(dir, "codex-existing.json")
	invalidPath := filepath.Join(dir, "kimi-invalid.json")
	if err := os.WriteFile(kimiPath, []byte(`{"type":"kimi","access_token":"do-not-log","custom":{"keep":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte(`{"type":"codex","priority":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalidPath, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}

	updated, err := EnsureCredentialPriority(dir)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	data, err := os.ReadFile(kimiPath)
	if err != nil {
		t.Fatal(err)
	}
	var credential map[string]any
	if err := json.Unmarshal(data, &credential); err != nil {
		t.Fatal(err)
	}
	if credential["priority"] != float64(DefaultCredentialPriority) {
		t.Fatalf("priority = %#v, want %d", credential["priority"], DefaultCredentialPriority)
	}
	if custom, ok := credential["custom"].(map[string]any); !ok || custom["keep"] != true {
		t.Fatalf("custom metadata was not preserved: %#v", credential["custom"])
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(kimiPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("migrated credential mode = %o, want 600", info.Mode().Perm())
		}
	}
	otherData, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(otherData) != `{"type":"codex","priority":7}` {
		t.Fatalf("non-Kimi credential changed: %s", otherData)
	}
	invalidData, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(invalidData) != `not-json` {
		t.Fatalf("invalid credential changed: %s", invalidData)
	}

	updated, err = EnsureCredentialPriority(dir)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 0 {
		t.Fatalf("second migration updated = %d, want 0", updated)
	}
}

func testClient(transport http.RoundTripper) *Client {
	return &Client{
		HTTPClient: &http.Client{Transport: transport, Timeout: time.Second}, OAuthBaseURL: "https://auth.test",
		DeviceID: "test-device-id", Version: "test-version", MinPollInterval: time.Millisecond,
		SlowDownIncrement: time.Millisecond, MaxPollDuration: time.Second,
	}
}

func jsonResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	var body strings.Builder
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body.String()))}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
