package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultKimiCodeModels(t *testing.T) {
	cfg := Default()
	if len(cfg.Providers) != 1 || cfg.Providers[0].ID != "kimi-code" || cfg.Providers[0].SetupURL != "https://www.kimi.com/code" {
		t.Fatalf("default providers = %#v", cfg.Providers)
	}
	if !strings.Contains(cfg.Providers[0].SetupHint, "API key") || !strings.Contains(cfg.Providers[0].SetupHint, "CLIProxyAPI") {
		t.Fatalf("default Kimi setup hint = %q", cfg.Providers[0].SetupHint)
	}
	want := map[string]struct {
		upstream string
		context  int64
	}{
		"kimi-k3": {upstream: "kimi-k3", context: 1_048_576},
	}
	if len(cfg.Models) != len(want) {
		t.Fatalf("default model count = %d, want %d", len(cfg.Models), len(want))
	}
	for _, model := range cfg.Models {
		expected, ok := want[model.ID]
		if !ok {
			t.Fatalf("unexpected default model %q", model.ID)
		}
		if model.UpstreamModel != expected.upstream || model.ContextWindow != expected.context {
			t.Fatalf("model %q routing/context = %q/%d", model.ID, model.UpstreamModel, model.ContextWindow)
		}
		if model.ProviderID != "kimi-code" {
			t.Fatalf("model %q provider = %q", model.ID, model.ProviderID)
		}
		if !reflect.DeepEqual(model.ReasoningLevels, []string{"none"}) {
			t.Fatalf("model %q reasoning levels = %#v", model.ID, model.ReasoningLevels)
		}
		if model.DefaultReasoningLevel != "none" {
			t.Fatalf("model %q default reasoning = %q", model.ID, model.DefaultReasoningLevel)
		}
		if !reflect.DeepEqual(model.ExcludedToolNamespacePrefixes, []string{"mcp__codex_apps__"}) {
			t.Fatalf("model %q excluded tool namespaces = %#v", model.ID, model.ExcludedToolNamespacePrefixes)
		}
	}
}

func TestLoadRemovesDeprecatedBundledKimi256KModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	data := []byte(`{
  "schema_version": 1,
  "models": [
    {
      "id": "kimi-k3-256k",
      "provider_id": "kimi-code",
      "upstream_model": "kimi-k3-256k",
      "context_window": 262144,
      "capabilities": {"streaming": true, "tools": true},
      "compatibility": {"status": "experimental", "cliproxyapi": "7.3.11"}
    },
    {
      "id": "kimi-k3",
      "provider_id": "kimi-code",
      "upstream_model": "kimi-k3",
      "context_window": 1048576,
      "input_modalities": ["text", "image"],
      "output_modalities": ["text"],
      "capabilities": {"streaming": true, "tools": true},
      "compatibility": {"status": "experimental", "cliproxyapi": "7.3.11"}
    }
  ]
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Models) != 1 || loaded.Models[0].ID != "kimi-k3" {
		t.Fatalf("models after migration = %#v", loaded.Models)
	}
}

func TestLoadNormalizesLegacyKimiReasoningLevels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	data := []byte(`{
  "schema_version": 1,
  "models": [
    {
      "id": "kimi-k3",
      "provider_id": "kimi-code",
      "upstream_model": "kimi-k3",
      "wire_api": "responses",
      "context_window": 1048576,
      "input_modalities": ["text"],
      "output_modalities": ["text"],
      "reasoning_levels": ["low", "high", "max"],
      "default_reasoning_level": "high",
      "reasoning_wire_format": "kimi-thinking",
      "capabilities": {"streaming": true, "tools": true}
    }
  ]
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Models) != 1 {
		t.Fatalf("loaded models = %#v", loaded.Models)
	}
	model := loaded.Models[0]
	if !reflect.DeepEqual(model.ReasoningLevels, []string{"none"}) || model.DefaultReasoningLevel != "none" {
		t.Fatalf("Kimi reasoning was not normalized: levels=%#v default=%q", model.ReasoningLevels, model.DefaultReasoningLevel)
	}
	if !reflect.DeepEqual(model.ExcludedToolNamespacePrefixes, []string{"mcp__codex_apps__"}) {
		t.Fatalf("Kimi excluded tool namespaces were not migrated: %#v", model.ExcludedToolNamespacePrefixes)
	}
}

func TestLoadMigratesLegacyKimiConsoleDeepLink(t *testing.T) {
	cfg := Default()
	cfg.Providers[0].SetupURL = "https://www.kimi.com/code/console"
	cfg.Providers[0].SetupHint = ""

	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := loaded.Provider("kimi-code")
	if !ok {
		t.Fatal("Kimi Code provider is missing")
	}
	if provider.SetupURL != "https://www.kimi.com/code" {
		t.Fatalf("migrated setup URL = %q", provider.SetupURL)
	}
	if !strings.Contains(provider.SetupHint, "API key") || !strings.Contains(provider.SetupHint, "CLIProxyAPI") {
		t.Fatalf("migrated setup hint = %q", provider.SetupHint)
	}
}

func TestLoadDerivesCLIProxyAuthDirForExistingConfigs(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gateway.json")
	proxyConfigPath := filepath.Join(dir, "custom-proxy", "config.yaml")
	wantAuthDir := filepath.Join(dir, "custom-proxy", "auth")
	if err := os.MkdirAll(filepath.Dir(proxyConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proxyConfigPath, []byte("auth-dir: \""+wantAuthDir+"\"\napi-keys:\n  - \"secret-not-read-by-resolver\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "schema_version": 1,
  "cliproxy_config_path": "` + proxyConfigPath + `"
}`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	gotAuthDir, err := cfg.ResolveCLIProxyAuthDir()
	if err != nil {
		t.Fatal(err)
	}
	if gotAuthDir != wantAuthDir {
		t.Fatalf("cliproxy auth dir = %q", gotAuthDir)
	}
}

func TestResolveCLIProxyAuthDirOverrideAndFallback(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.CLIProxyConfigPath = filepath.Join(dir, "config.yaml")
	if got, err := cfg.ResolveCLIProxyAuthDir(); err != nil || got != dir {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	cfg.CLIProxyAuthDir = filepath.Join(dir, "override")
	if got, err := cfg.ResolveCLIProxyAuthDir(); err != nil || got != cfg.CLIProxyAuthDir {
		t.Fatalf("override = %q, %v", got, err)
	}
}

func TestLoadMigratesLegacyConfigInMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{
  "models": [{
    "id": "legacy-model",
    "display_name": "Legacy",
    "context_window": 32000,
    "input_modalities": ["text"],
    "reasoning_levels": ["none"]
  }]
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema version = %d", cfg.SchemaVersion)
	}
	model := cfg.Models[0]
	if model.Route != "cliproxy" || model.UpstreamModel != "legacy-model" || model.WireAPI != "responses" {
		t.Fatalf("legacy defaults = %#v", model)
	}
	if !model.Capabilities.Streaming || !model.Capabilities.Tools {
		t.Fatalf("legacy capabilities were not preserved: %#v", model.Capabilities)
	}
}

func TestLoadRejectsUnknownFieldAndFutureSchema(t *testing.T) {
	dir := t.TempDir()
	unknownPath := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(unknownPath, []byte(`{"schema_version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(unknownPath); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	futurePath := filepath.Join(dir, "future.json")
	if err := os.WriteFile(futurePath, []byte(`{"schema_version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(futurePath); err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("future schema error = %v", err)
	}
}

func TestValidateRejectsUnsafeListenAndInvalidCapabilities(t *testing.T) {
	cfg := Default()
	cfg.Listen = "0.0.0.0:8765"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("unsafe listen error = %v", err)
	}
	cfg.AllowNonLoopback = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("explicit non-loopback opt-in: %v", err)
	}

	cfg = Default()
	cfg.Models[0].Capabilities.Streaming = false
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "streaming must be true") {
		t.Fatalf("streaming error = %v", err)
	}

	cfg = Default()
	cfg.Models = append(cfg.Models, cfg.Models[0])
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate model error = %v", err)
	}
}

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
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
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
