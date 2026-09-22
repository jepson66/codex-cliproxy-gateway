package diagnostics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-cliproxy-gateway/internal/config"
)

func TestRunChecksReadinessAndOptionalE2E(t *testing.T) {
	var e2eModel string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer local-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/models":
			return diagnosticResponse(http.StatusOK, `{"data":[]}`), nil
		case "/v1/responses":
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			e2eModel = payload.Model
			return diagnosticResponse(http.StatusOK, `{"id":"response-test","output":[]}`), nil
		default:
			return diagnosticResponse(http.StatusNotFound, `{}`), nil
		}
	})}

	cfg := testConfig(t)
	cfg.CLIProxyBaseURL = "http://cliproxy.test/v1"
	cfg.CLIProxyAPIKeyEnv = "TEST_DIAGNOSTIC_KEY"
	t.Setenv("TEST_DIAGNOSTIC_KEY", "local-key")
	results := (Runner{Client: client}).Run(context.Background(), cfg, true, "cliproxy/kimi-k3")
	for _, result := range results {
		if result.Err != nil {
			t.Fatalf("%s: %v", result.Name, result.Err)
		}
	}
	if e2eModel != "kimi-k3" {
		t.Fatalf("e2e model = %q", e2eModel)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func diagnosticResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(cachePath, []byte(`{"models":[{"slug":"gpt-test"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	zstdPath := filepath.Join(dir, "zstd")
	if err := os.WriteFile(zstdPath, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.OfficialModelsCache = cachePath
	cfg.ZstdCommand = zstdPath
	return cfg
}
