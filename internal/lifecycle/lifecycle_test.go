package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-cliproxy-gateway/internal/config"
)

func TestPlanExplainsConfigScopeAndManagedDependency(t *testing.T) {
	cfg := config.Default()
	plan, err := BuildPlan(cfg, config.DefaultPath(), Managed)
	if err != nil {
		t.Fatal(err)
	}
	text := plan.String()
	for _, expected := range []string{
		"download/verify/install",
		"SHA-256 pinned",
		"full replacement of ~/.codex/config.toml: no",
		"provider API keys printed or stored in Git: no",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("plan missing %q:\n%s", expected, text)
		}
	}
}

func TestEnsureManagedCLIProxyConfigCreatesPrivateFilesAndPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.CLIProxyConfigPath = filepath.Join(dir, "cliproxy", "config.yaml")
	cfg.CLIProxyAPIKeyFile = filepath.Join(dir, "gateway", "key")
	created, err := EnsureManagedCLIProxyConfig(cfg)
	if err != nil || !created {
		t.Fatalf("create = %t, %v", created, err)
	}
	configData, err := os.ReadFile(cfg.CLIProxyConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	keyData, err := os.ReadFile(cfg.CLIProxyAPIKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), strings.TrimSpace(string(keyData))) {
		t.Fatal("Gateway key and CLIProxyAPI access key differ")
	}
	for _, path := range []string{cfg.CLIProxyConfigPath, cfg.CLIProxyAPIKeyFile} {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", path, info.Mode().Perm())
		}
	}
	if err := os.WriteFile(cfg.CLIProxyConfigPath, []byte("preserve-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err = EnsureManagedCLIProxyConfig(cfg)
	if err != nil || created {
		t.Fatalf("second create = %t, %v", created, err)
	}
	if data, _ := os.ReadFile(cfg.CLIProxyConfigPath); string(data) != "preserve-me\n" {
		t.Fatalf("existing config was overwritten: %q", data)
	}
}
