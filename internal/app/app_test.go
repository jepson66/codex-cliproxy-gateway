package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex-cliproxy-gateway/internal/config"
	"codex-cliproxy-gateway/internal/kimioauth"
	"codex-cliproxy-gateway/internal/providerauth"
)

func TestRunUsageAndParseExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing command", nil, "usage:"},
		{"unknown command", []string{"unknown"}, "usage:"},
		{"unknown flag", []string{"version", "--unknown"}, "flag provided but not defined"},
		{"positional argument", []string{"version", "extra"}, "unexpected positional arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), test.args, Streams{Err: &stderr})
			if exitCode != 2 {
				t.Fatalf("exit code = %d, stderr = %s", exitCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr missing %q: %s", test.want, stderr.String())
			}
		})
	}
}

func TestVersionDoesNotLoadConfiguration(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(configPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"version", "--config", configPath}, Streams{Out: &stdout, Err: &stderr})
	if exitCode != 0 || strings.TrimSpace(stdout.String()) != Version || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestInitCreatesThenPreservesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nested", "gateway.json")
	for attempt, expected := range []string{"created", "preserved existing"} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(context.Background(), []string{"init", "--config", configPath}, Streams{Out: &stdout, Err: &stderr})
		if exitCode != 0 || !strings.Contains(stdout.String(), expected) || stderr.Len() != 0 {
			t.Fatalf("attempt %d: exit = %d, stdout = %q, stderr = %q", attempt, exitCode, stdout.String(), stderr.String())
		}
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v", info.Mode().Perm())
	}
}

func TestBootstrapRequiresExplicitNonInteractiveConfirmation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "gateway.json")
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"bootstrap", "--config", configPath}, Streams{
		Out:        &stdout,
		Err:        &stderr,
		IsTerminal: func() bool { return false },
	})
	if exitCode != 1 || !strings.Contains(stderr.String(), "confirmation required") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap wrote config before confirmation: %v", err)
	}
}

func TestStatusReportsCleanUserWithoutMutatingIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "LocalAppData"))
	configPath := filepath.Join(dir, "invalid-config.json")
	if err := os.WriteFile(configPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"status", "--config", configPath}, Streams{Out: &stdout, Err: &stderr})
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
	for _, expected := range []string{"Service manager:", "Gateway:", "Managed CLIProxyAPI:", "state: not-installed"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("status missing %q:\n%s", expected, stdout.String())
		}
	}
	if data, err := os.ReadFile(configPath); err != nil || string(data) != "not-json" {
		t.Fatalf("status mutated config: %q, %v", data, err)
	}
}

func TestConfirmHandlesInteractiveAnswers(t *testing.T) {
	for _, test := range []struct {
		answer  string
		wantErr bool
	}{
		{"yes\n", false},
		{"Y\n", false},
		{"no\n", true},
		{"", true},
	} {
		var output bytes.Buffer
		err := confirm(Streams{
			In:         strings.NewReader(test.answer),
			Out:        &output,
			IsTerminal: func() bool { return true },
		}, "Apply?", false)
		if (err != nil) != test.wantErr {
			t.Errorf("answer %q: error = %v", test.answer, err)
		}
		if !strings.Contains(output.String(), "Apply? [y/N]") {
			t.Errorf("answer %q: prompt = %q", test.answer, output.String())
		}
	}
}

func TestProviderLoginPerformsKimiOAuthAndPreservesAPIKeyCompatibility(t *testing.T) {
	cfg := config.Default()
	cfg.CLIProxyBaseURL = "http://cliproxy.test/v1"
	cfg.CLIProxyConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	cfg.CLIProxyAuthDir = filepath.Join(t.TempDir(), "auth")
	cfg.CLIProxyAPIKeyEnv = "TEST_APP_PROVIDER_KEY"
	t.Setenv("TEST_APP_PROVIDER_KEY", "local-key")

	modelsBody := `{"data":[{"id":"other-model"}]}`
	checker := providerauth.Checker{Client: &http.Client{Transport: appRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(modelsBody))}, nil
	})}}
	var stdout, stderr bytes.Buffer
	opened := ""
	deps := providerCommandDependencies{
		checker:   checker,
		kimiOAuth: &fakeKimiFlow{},
		openURL: func(url string) error {
			opened = url
			return nil
		},
		saveCredential: func(dir string, token kimioauth.Token, deviceID string, now time.Time) (string, error) {
			path, err := kimioauth.SaveCredential(dir, token, deviceID, now)
			if err == nil {
				modelsBody = `{"data":[{"id":"kimi-k3"}]}`
			}
			return path, err
		},
		now: func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
	}
	if err := runProviderCommand(context.Background(), Streams{Out: &stdout, Err: &stderr}, cfg, "kimi-code", true, false, deps); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"one-time Kimi authorization URL", "https://auth.kimi.test/activate?code=ABCD-EFGH", "User code: ABCD-EFGH", "Waiting for Kimi authorization", "saved securely", "configured and available"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("login output missing %q: %s", expected, stdout.String())
		}
	}
	if opened != "https://auth.kimi.test/activate?code=ABCD-EFGH" {
		t.Fatalf("opened URL = %q", opened)
	}
	if strings.Contains(stdout.String(), "secret-access-token") || strings.Contains(stderr.String(), "secret-access-token") {
		t.Fatal("OAuth token leaked to command output")
	}
	entries, err := os.ReadDir(cfg.CLIProxyAuthDir)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "kimi-") {
		t.Fatalf("credential files = %#v, error = %v", entries, err)
	}

	stdout.Reset()
	if err := runProviderCommand(context.Background(), Streams{Out: &stdout, Err: &stderr}, cfg, "kimi-code", false, true, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "is configured in CLIProxyAPI") {
		t.Fatalf("configured output = %s", stdout.String())
	}
}

func TestKimiOAuthNoBrowserDoesNotOpenURL(t *testing.T) {
	cfg := config.Default()
	cfg.CLIProxyBaseURL = "http://cliproxy.test/v1"
	cfg.CLIProxyAuthDir = t.TempDir()
	cfg.CLIProxyAPIKeyEnv = "TEST_APP_NO_BROWSER_KEY"
	t.Setenv("TEST_APP_NO_BROWSER_KEY", "local-key")
	modelsBody := `{"data":[{"id":"other-model"}]}`
	checker := providerauth.Checker{Client: &http.Client{Transport: appRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(modelsBody))}, nil
	})}}
	opened := false
	deps := providerCommandDependencies{
		checker: checker, kimiOAuth: &fakeKimiFlow{},
		openURL: func(string) error { opened = true; return nil },
		saveCredential: func(dir string, token kimioauth.Token, deviceID string, now time.Time) (string, error) {
			modelsBody = `{"data":[{"id":"kimi-k3"}]}`
			return kimioauth.SaveCredential(dir, token, deviceID, now)
		},
	}
	if err := runProviderCommand(context.Background(), Streams{Out: io.Discard, Err: io.Discard}, cfg, "kimi-code", true, true, deps); err != nil {
		t.Fatal(err)
	}
	if opened {
		t.Fatal("browser opener was called with --no-browser")
	}
}

type fakeKimiFlow struct{}

func (*fakeKimiFlow) RequestDeviceCode(context.Context) (kimioauth.DeviceCode, error) {
	return kimioauth.DeviceCode{DeviceCode: "device-secret", UserCode: "ABCD-EFGH", VerificationURIComplete: "https://auth.kimi.test/activate?code=ABCD-EFGH", ExpiresIn: 60}, nil
}

func (*fakeKimiFlow) PollForToken(context.Context, kimioauth.DeviceCode) (kimioauth.Token, error) {
	return kimioauth.Token{AccessToken: "secret-access-token", RefreshToken: "secret-refresh-token", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (*fakeKimiFlow) DeviceIdentifier() string { return "device-id" }

type appRoundTripFunc func(*http.Request) (*http.Response, error)

func (f appRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
