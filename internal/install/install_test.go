package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-cliproxy-gateway/internal/config"
)

func TestPatchConfigPreservesOtherSettingsAndIsIdempotent(t *testing.T) {
	cfg := config.Default()
	cfg.ModelCatalogPath = "/tmp/catalog.json"
	input := `model = "gpt-test"
model_provider = "old"
model_catalog_json = "/tmp/old.json"
approval_policy = "never"

[features]
apps = true

[model_providers.codex-cliproxy-gateway]
name = "stale"
base_url = "http://stale"

[model_providers.openai-http]
name = "OpenAI HTTP"
base_url = "https://chatgpt.com/backend-api/codex"
wire_api = "responses"
requires_openai_auth = true

[model_providers.other]
base_url = "https://other.invalid/v1"

[mcp_servers.example]
url = "https://example.invalid"
`
	patched := PatchConfig(input, cfg)
	patchedAgain := PatchConfig(patched, cfg)
	if patchedAgain != patched {
		t.Fatalf("PatchConfig is not idempotent:\n%s", patchedAgain)
	}
	for _, preserved := range []string{`model = "gpt-test"`, `approval_policy = "never"`, `[features]`, `apps = true`, `[mcp_servers.example]`} {
		if !strings.Contains(patched, preserved) {
			t.Fatalf("missing preserved setting %q:\n%s", preserved, patched)
		}
	}
	if strings.Count(patched, managedComment) != 1 {
		t.Fatalf("managed comment count = %d", strings.Count(patched, managedComment))
	}
	if strings.Count(patched, "[model_providers."+providerID+"]") != 1 {
		t.Fatalf("provider table count != 1:\n%s", patched)
	}
	if !strings.Contains(patched, `base_url = "http://127.0.0.1:8765/v1"`) {
		t.Fatalf("gateway base URL missing:\n%s", patched)
	}
	if strings.Contains(patched, `base_url = "https://chatgpt.com/backend-api/codex"`) {
		t.Fatalf("legacy provider still bypasses gateway:\n%s", patched)
	}
	if !strings.Contains(patched, `[model_providers.other]
base_url = "https://other.invalid/v1"`) {
		t.Fatalf("unrelated provider was changed:\n%s", patched)
	}
}

func TestPatchConfigPrefixesAnExistingThirdPartySelection(t *testing.T) {
	cfg := config.Default()
	patched := PatchConfig("model = \"kimi-k3\"\nmodel_reasoning_effort = \"none\"\n", cfg)
	if !strings.Contains(patched, `model = "cliproxy/kimi-k3"`) {
		t.Fatalf("selected model was not namespaced:\n%s", patched)
	}
	if !strings.Contains(patched, `model_reasoning_effort = "none"`) {
		t.Fatalf("reasoning setting was not preserved:\n%s", patched)
	}
}

func TestApplyTwiceKeepsOriginalBackupAndRestoreProtectsUserChanges(t *testing.T) {
	cfg := testConfig(t)
	original := "model = \"gpt-test\"\napproval_policy = \"never\"\n"
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := Apply(cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Apply(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first.BackupPath != second.BackupPath {
		t.Fatalf("repeat install replaced backup: %q != %q", first.BackupPath, second.BackupPath)
	}
	backup, err := os.ReadFile(first.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatalf("backup changed: %q", backup)
	}

	installed, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, append(installed, []byte("# user change\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(cfg); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("Restore error = %v", err)
	}
	after, _ := os.ReadFile(configPath)
	if !strings.Contains(string(after), "# user change") {
		t.Fatal("Restore overwrote a user change")
	}
}

func TestApplyAllowsModelSelectionChanges(t *testing.T) {
	cfg := testConfig(t)
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-test\"\nmodel_reasoning_effort = \"medium\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(installed), `model = "gpt-test"`, `model = "cliproxy/kimi-k3"`, 1)
	changed = strings.Replace(changed, `model_reasoning_effort = "medium"`, `model_reasoning_effort = "none"`, 1)
	if err := os.WriteFile(configPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg); err != nil {
		t.Fatalf("normal /model changes blocked update: %v", err)
	}
}

func TestApplyMigratesLegacyRawHashAfterModelSelectionChange(t *testing.T) {
	cfg := testConfig(t)
	original := "model = \"gpt-test\"\nmodel_reasoning_effort = \"medium\"\n"
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := Apply(cfg)
	if err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(configPath)
	state.HashMode = ""
	state.InstalledHash = hash(installed)
	if err := saveState(cfg, state); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(installed), `model = "gpt-test"`, `model = "cliproxy/kimi-k3"`, 1)
	changed = strings.Replace(changed, `model_reasoning_effort = "medium"`, `model_reasoning_effort = "none"`, 1)
	if err := os.WriteFile(configPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg); err != nil {
		t.Fatalf("legacy install state was not migrated: %v", err)
	}
}

func TestApplyAndRestoreRoundTrip(t *testing.T) {
	cfg := testConfig(t)
	original := "model = \"gpt-test\"\n"
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Restore(cfg); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(configPath)
	if string(restored) != original {
		t.Fatalf("restored config = %q", restored)
	}
}

func TestApplyValidatesComplexTOMLWithoutReformatting(t *testing.T) {
	cfg := testConfig(t)
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	original := `# preserve this comment
model = "gpt-test"
features = ["one", "two"]

[model_providers."codex-cliproxy-gateway"]
name = "stale"
base_url = "http://stale.invalid"

[projects."/Volumes/OWC/path with spaces"]
trust_level = "trusted"
`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{
		"# preserve this comment",
		`features = ["one", "two"]`,
		`[projects."/Volumes/OWC/path with spaces"]`,
	} {
		if !strings.Contains(string(got), preserved) {
			t.Fatalf("missing preserved TOML %q:\n%s", preserved, got)
		}
	}
	if strings.Count(string(got), "codex-cliproxy-gateway]") != 1 {
		t.Fatalf("target provider was duplicated:\n%s", got)
	}
}

func TestApplyRejectsInvalidExistingTOML(t *testing.T) {
	cfg := testConfig(t)
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	original := "model = [unterminated\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(cfg); err == nil || !strings.Contains(err.Error(), "invalid TOML") {
		t.Fatalf("invalid TOML error = %v", err)
	}
	got, _ := os.ReadFile(configPath)
	if string(got) != original {
		t.Fatalf("invalid config was modified: %q", got)
	}
}

func TestSummaryExplainsManagedChangesAndMaintenance(t *testing.T) {
	cfg := config.Default()
	state := State{BackupPath: "/tmp/config.toml.backup"}
	got := Summary(cfg, state)
	for _, expected := range []string{
		"full config.toml replacement: no",
		`model_provider = "codex-cliproxy-gateway"`,
		`model_catalog_json = "` + cfg.ModelCatalogPath + `"`,
		"[model_providers.codex-cliproxy-gateway]",
		"openai-http",
		"codex-cliproxy-gateway catalog",
		"codex-cliproxy-gateway service-install",
		"codex-cliproxy-gateway uninstall",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("summary missing %q:\n%s", expected, got)
		}
	}
}

func TestRepairLegacyProvidersPreservesUserConfigAndCreatesBackup(t *testing.T) {
	cfg := testConfig(t)
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	original := `model = "cliproxy/kimi-k3"
model_reasoning_effort = "none"

[projects."/Volumes/OWC/example"]
trust_level = "trusted"

[model_providers.openai-http]
name = "OpenAI HTTP"
base_url = "https://chatgpt.com/backend-api/codex"
wire_api = "responses"
requires_openai_auth = true

[model_providers.other]
base_url = "https://other.invalid/v1"

[mcp_servers.example]
url = "https://example.invalid"
`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	backupPath, changed, err := RepairLegacyProviders(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("repair reported no change")
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != original {
		t.Fatalf("backup differs from original:\n%s", backup)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(original,
		`base_url = "https://chatgpt.com/backend-api/codex"`,
		`base_url = "http://127.0.0.1:8765/v1"`, 1)
	if string(got) != want {
		t.Fatalf("repair changed more than the legacy base URL:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRepairLegacyProvidersAddsMissingBaseURLAndIsIdempotent(t *testing.T) {
	cfg := testConfig(t)
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	original := `[model_providers.openai-http]
name = "OpenAI HTTP"
wire_api = "responses"

[features]
apps = true
`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	_, changed, err := RepairLegacyProviders(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("repair reported no change")
	}
	first, _ := os.ReadFile(configPath)
	if !strings.Contains(string(first), `[model_providers.openai-http]
name = "OpenAI HTTP"
wire_api = "responses"
base_url = "http://127.0.0.1:8765/v1"

[features]`) {
		t.Fatalf("missing legacy base URL was not added inside its table:\n%s", first)
	}

	backupPath, changed, err := RepairLegacyProviders(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second repair should be a no-op")
	}
	if backupPath != "" {
		t.Fatalf("no-op repair created backup %q", backupPath)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "models_cache.json")
	if err := os.WriteFile(cachePath, []byte(`{"models":[{"slug":"gpt-test","display_name":"GPT Test"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.CodexHome = dir
	cfg.OfficialModelsCache = cachePath
	cfg.ModelCatalogPath = filepath.Join(dir, "model-catalogs", "gateway.json")
	return cfg
}
