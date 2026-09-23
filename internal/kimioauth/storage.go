package kimioauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultCredentialPriority keeps Kimi Code OAuth ahead of API-key fallbacks.
// CLIProxyAPI assigns providers without an explicit priority to priority 0.
const DefaultCredentialPriority = 100

type credentialFile struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
	Expired      string `json:"expired,omitempty"`
	Type         string `json:"type"`
	Domain       string `json:"domain"`
	BaseURL      string `json:"base_url"`
	Timestamp    int64  `json:"timestamp"`
	Disabled     bool   `json:"disabled"`
	Priority     int    `json:"priority"`
}

func SaveCredential(authDir string, token Token, deviceID string, now time.Time) (string, error) {
	authDir = filepath.Clean(strings.TrimSpace(authDir))
	if authDir == "." || authDir == "" {
		return "", errors.New("CLIProxyAPI auth directory is required")
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return "", errors.New("Kimi access token is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	tokenType := strings.TrimSpace(token.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	credential := credentialFile{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    tokenType,
		Scope:        token.Scope,
		DeviceID:     strings.TrimSpace(deviceID),
		Type:         "kimi",
		Domain:       "kimi.com",
		BaseURL:      APIBaseURL,
		Timestamp:    now.UnixMilli(),
		Disabled:     false,
		Priority:     DefaultCredentialPriority,
	}
	if !token.ExpiresAt.IsZero() {
		credential.Expired = token.ExpiresAt.UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode Kimi credential: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return "", fmt.Errorf("create CLIProxyAPI auth directory: %w", err)
	}
	if err := os.Chmod(authDir, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		return "", fmt.Errorf("protect CLIProxyAPI auth directory: %w", err)
	}
	path := filepath.Join(authDir, fmt.Sprintf("kimi-%d.json", now.UnixMilli()))
	temporary, err := os.CreateTemp(authDir, ".kimi-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary Kimi credential: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("protect temporary Kimi credential: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return "", fmt.Errorf("write temporary Kimi credential: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync temporary Kimi credential: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close temporary Kimi credential: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("commit Kimi credential: %w", err)
	}
	committed = true
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		return "", fmt.Errorf("protect Kimi credential: %w", err)
	}
	return path, nil
}

// EnsureCredentialPriority upgrades existing Kimi OAuth files created before
// priority routing was introduced. It never prints or returns credential data.
func EnsureCredentialPriority(authDir string) (int, error) {
	authDir = filepath.Clean(strings.TrimSpace(authDir))
	if authDir == "." || authDir == "" {
		return 0, errors.New("CLIProxyAPI auth directory is required")
	}
	entries, err := os.ReadDir(authDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read CLIProxyAPI auth directory: %w", err)
	}
	updated := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(strings.ToLower(entry.Name()), "kimi-") || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(authDir, entry.Name())
		changed, err := ensureFilePriority(path)
		if err != nil {
			return updated, err
		}
		if changed {
			updated++
		}
	}
	return updated, nil
}

func ensureFilePriority(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read Kimi credential %s: %w", filepath.Base(path), err)
	}
	var credential map[string]json.RawMessage
	if err := json.Unmarshal(data, &credential); err != nil {
		// CLIProxyAPI ignores malformed credential files; leave them untouched too.
		return false, nil
	}
	var credentialType string
	if rawType, ok := credential["type"]; !ok || json.Unmarshal(rawType, &credentialType) != nil || !strings.EqualFold(strings.TrimSpace(credentialType), "kimi") {
		return false, nil
	}
	if rawPriority, ok := credential["priority"]; ok {
		var priority int
		if err := json.Unmarshal(rawPriority, &priority); err == nil && priority >= DefaultCredentialPriority {
			return false, nil
		}
		var textPriority string
		if err := json.Unmarshal(rawPriority, &textPriority); err == nil {
			if priority, err := strconv.Atoi(strings.TrimSpace(textPriority)); err == nil && priority >= DefaultCredentialPriority {
				return false, nil
			}
		}
	}
	credential["priority"] = json.RawMessage(strconv.Itoa(DefaultCredentialPriority))
	encoded, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode Kimi credential metadata: %w", err)
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kimi-priority-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temporary Kimi credential: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return false, fmt.Errorf("protect temporary Kimi credential: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		return false, fmt.Errorf("write temporary Kimi credential: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return false, fmt.Errorf("sync temporary Kimi credential: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close temporary Kimi credential: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, fmt.Errorf("commit Kimi credential priority: %w", err)
	}
	committed = true
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		return false, fmt.Errorf("protect migrated Kimi credential: %w", err)
	}
	return true, nil
}
