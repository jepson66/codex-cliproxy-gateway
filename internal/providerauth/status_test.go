package providerauth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"codex-cliproxy-gateway/internal/config"
)

func TestCheckUsesCLIProxyCatalogWithoutReadingProviderKey(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		configured bool
	}{
		{"configured", `{"data":[{"id":"kimi-k3"}]}`, true},
		{"missing", `{"data":[{"id":"other-model"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer local-key" {
					t.Fatalf("request = %s, authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
				}
				return response(http.StatusOK, test.body), nil
			})}
			cfg := config.Default()
			cfg.CLIProxyBaseURL = "http://cliproxy.test/v1"
			cfg.CLIProxyAPIKeyEnv = "TEST_PROVIDER_AUTH_KEY"
			t.Setenv("TEST_PROVIDER_AUTH_KEY", "local-key")
			status, err := (Checker{Client: client}).Check(context.Background(), cfg, "kimi-code", "kimi-k3")
			if err != nil {
				t.Fatal(err)
			}
			if status.Configured != test.configured {
				t.Fatalf("configured = %v", status.Configured)
			}
			if status.ProviderConfigured != test.configured {
				t.Fatalf("provider configured = %v", status.ProviderConfigured)
			}
			if status.Provider.ID != "kimi-code" {
				t.Fatalf("provider = %#v", status.Provider)
			}
		})
	}
}

func TestCheckClassifiesCLIProxyFailures(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusUnauthorized, `{"error":"bad local key"}`), nil
	})}
	cfg := config.Default()
	cfg.CLIProxyBaseURL = "http://cliproxy.test/v1"
	cfg.CLIProxyAPIKeyEnv = "TEST_PROVIDER_AUTH_KEY"
	t.Setenv("TEST_PROVIDER_AUTH_KEY", "local-key")
	if _, err := (Checker{Client: client}).Check(context.Background(), cfg, "kimi-code", ""); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
