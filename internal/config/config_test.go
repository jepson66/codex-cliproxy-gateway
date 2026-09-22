package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCLIProxyAPIKeyPrefersEnvironmentThenFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	if err := os.WriteFile(keyPath, []byte("file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.CLIProxyAPIKeyEnv = "TEST_CLIPROXY_KEY"
	cfg.CLIProxyAPIKeyFile = keyPath
	t.Setenv("TEST_CLIPROXY_KEY", "env-key")
	key, err := cfg.ResolveCLIProxyAPIKey()
	if err != nil || key != "env-key" {
		t.Fatalf("environment resolution = %q, %v", key, err)
	}
	t.Setenv("TEST_CLIPROXY_KEY", "")
	key, err = cfg.ResolveCLIProxyAPIKey()
	if err != nil || key != "file-key" {
		t.Fatalf("file resolution = %q, %v", key, err)
	}
}

func TestImportCLIProxyAPIKey(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	keyPath := filepath.Join(dir, "private", "key")
	yaml := "host: 127.0.0.1\napi-keys:\n  - \"local-secret\"\nopenai-compatibility: []\n"
	if err := os.WriteFile(configPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.CLIProxyConfigPath = configPath
	cfg.CLIProxyAPIKeyFile = keyPath
	if err := cfg.ImportCLIProxyAPIKey(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local-secret\n" {
		t.Fatalf("imported key = %q", data)
	}
	info, _ := os.Stat(keyPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %o", info.Mode().Perm())
	}
}

func TestResolveConfiguredZstdCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zstd")
	if err := os.WriteFile(path, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.ZstdCommand = path
	resolved, err := cfg.ResolveZstdCommand()
	if err != nil || resolved != path {
		t.Fatalf("zstd resolution = %q, %v", resolved, err)
	}
}
