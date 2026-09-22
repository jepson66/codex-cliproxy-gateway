package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
