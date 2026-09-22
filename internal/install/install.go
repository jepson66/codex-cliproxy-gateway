package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex-cliproxy-gateway/internal/catalog"
	"codex-cliproxy-gateway/internal/config"
	"github.com/pelletier/go-toml/v2"
)

const providerID = "codex-cliproxy-gateway"

const managedComment = "# Managed by codex-cliproxy-gateway. Use its uninstall command to restore the backup."

const normalizedHashMode = "ignore-model-selection-v1"

type State struct {
	ConfigPath    string `json:"config_path"`
	BackupPath    string `json:"backup_path"`
	InstalledHash string `json:"installed_hash"`
	HashMode      string `json:"hash_mode,omitempty"`
	InstalledAt   string `json:"installed_at"`
}

func Apply(cfg config.Config) (State, error) {
	if err := catalog.Write(cfg); err != nil {
		return State{}, err
	}
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	original, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return State{}, fmt.Errorf("read Codex config: %w", err)
	}
	if err := validateTOML("existing Codex config", original); err != nil {
		return State{}, err
	}
	if previous, stateErr := loadState(cfg); stateErr == nil {
		if !matchesInstalledConfig(original, previous, cfg) {
			return State{}, fmt.Errorf("Codex config changed after installation; refusing to replace it automatically (backup: %s). Run `catalog` to update only the generated model catalog, or reconcile config.toml manually before reinstalling", previous.BackupPath)
		}
		patched := PatchConfig(string(original), cfg)
		if err := validateTOML("patched Codex config", []byte(patched)); err != nil {
			return State{}, err
		}
		if err := writeFileAtomic(configPath, []byte(patched), 0o600); err != nil {
			return State{}, fmt.Errorf("update Codex config: %w", err)
		}
		previous.InstalledHash = managedHash([]byte(patched))
		previous.HashMode = normalizedHashMode
		previous.InstalledAt = time.Now().UTC().Format(time.RFC3339)
		if err := saveState(cfg, previous); err != nil {
			return State{}, err
		}
		return previous, nil
	}
	backupDir := filepath.Join(cfg.CodexHome, "codex-cliproxy-gateway", "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return State{}, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath := filepath.Join(backupDir, "config.toml."+stamp+".bak")
	if err := os.WriteFile(backupPath, original, 0o600); err != nil {
		return State{}, fmt.Errorf("write backup: %w", err)
	}

	patched := PatchConfig(string(original), cfg)
	if err := validateTOML("patched Codex config", []byte(patched)); err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(cfg.CodexHome, 0o700); err != nil {
		return State{}, err
	}
	if err := writeFileAtomic(configPath, []byte(patched), 0o600); err != nil {
		return State{}, fmt.Errorf("write Codex config: %w", err)
	}
	state := State{
		ConfigPath:    configPath,
		BackupPath:    backupPath,
		InstalledHash: managedHash([]byte(patched)),
		HashMode:      normalizedHashMode,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveState(cfg, state); err != nil {
		return State{}, err
	}
	return state, nil
}

// Summary explains the exact user configuration surface managed by Apply.
// Keep this output explicit because config.toml can contain unrelated user
// settings that the installer must never imply it owns.
func Summary(cfg config.Config, state State) string {
	lines := []string{
		"Codex config update:",
		"  full config.toml replacement: no (unrelated settings are preserved)",
		fmt.Sprintf("  backup: %s", state.BackupPath),
		fmt.Sprintf("  manages top-level model_provider = %q", providerID),
		fmt.Sprintf("  manages top-level model_catalog_json = %q", cfg.ModelCatalogPath),
		fmt.Sprintf("  manages table [model_providers.%s]", providerID),
	}
	if len(cfg.LegacyProviderIDs) > 0 {
		lines = append(lines, fmt.Sprintf("  redirects legacy provider base_url entries: %s", strings.Join(cfg.LegacyProviderIDs, ", ")))
	}
	lines = append(lines,
		"Maintenance:",
		"  after changing models: run `codex-cliproxy-gateway catalog`, then restart Codex",
		"  after updating this binary: run `codex-cliproxy-gateway service-install`",
		"  restore the pre-install config: run `codex-cliproxy-gateway uninstall`",
	)
	return strings.Join(lines, "\n")
}

func Restore(cfg config.Config) error {
	state, err := loadState(cfg)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(state.ConfigPath)
	if err != nil {
		return err
	}
	if !matchesInstalledConfig(current, state, cfg) {
		return fmt.Errorf("Codex config changed after installation; refusing to overwrite it automatically (backup: %s)", state.BackupPath)
	}
	backup, err := os.ReadFile(state.BackupPath)
	if err != nil {
		return err
	}
	if err := validateTOML("Codex config backup", backup); err != nil {
		return err
	}
	if err := writeFileAtomic(state.ConfigPath, backup, 0o600); err != nil {
		return err
	}
	return os.Remove(statePath(cfg))
}

// RepairLegacyProviders redirects provider IDs already persisted in older
// Codex threads through the Gateway without rewriting any other Codex setting.
// It is intentionally separate from Apply so an existing installation can be
// migrated even when the user has legitimately changed config.toml since the
// original install.
func RepairLegacyProviders(cfg config.Config) (backupPath string, changed bool, err error) {
	configPath := filepath.Join(cfg.CodexHome, "config.toml")
	original, err := os.ReadFile(configPath)
	if err != nil {
		return "", false, fmt.Errorf("read Codex config: %w", err)
	}
	if err := validateTOML("existing Codex config", original); err != nil {
		return "", false, err
	}
	patched, changed := patchLegacyProvidersOnly(string(original), cfg)
	if !changed {
		return "", false, nil
	}
	if err := validateTOML("patched Codex config", []byte(patched)); err != nil {
		return "", false, err
	}

	backupDir := filepath.Join(cfg.CodexHome, "codex-cliproxy-gateway", "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", false, fmt.Errorf("create backup directory: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath = filepath.Join(backupDir, "config.toml."+stamp+".pre-legacy-repair.bak")
	if err := os.WriteFile(backupPath, original, 0o600); err != nil {
		return "", false, fmt.Errorf("write repair backup: %w", err)
	}
	if err := writeFileAtomic(configPath, []byte(patched), 0o600); err != nil {
		return backupPath, false, fmt.Errorf("write repaired Codex config: %w", err)
	}
	return backupPath, true, nil
}

func patchLegacyProvidersOnly(input string, cfg config.Config) (string, bool) {
	lines := strings.Split(input, "\n")
	out := make([]string, 0, len(lines)+len(cfg.LegacyProviderIDs))
	inLegacyProvider := false
	legacyBaseURLSeen := false
	changed := false
	gatewayURL := "http://" + cfg.Listen + "/v1"

	flushLegacyProvider := func() {
		if !inLegacyProvider || legacyBaseURLSeen {
			return
		}
		insertAt := len(out)
		for insertAt > 0 && strings.TrimSpace(out[insertAt-1]) == "" {
			insertAt--
		}
		out = append(out, "")
		copy(out[insertAt+1:], out[insertAt:])
		out[insertAt] = fmt.Sprintf("base_url = %q", gatewayURL)
		legacyBaseURLSeen = true
		changed = true
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			flushLegacyProvider()
			inLegacyProvider = isLegacyProviderTable(trimmed, cfg.LegacyProviderIDs)
			legacyBaseURLSeen = false
		}
		if inLegacyProvider && isTopLevelAssignment(trimmed, "base_url") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			rewritten := indent + fmt.Sprintf("base_url = %q", gatewayURL)
			out = append(out, rewritten)
			legacyBaseURLSeen = true
			if rewritten != line {
				changed = true
			}
			continue
		}
		out = append(out, line)
	}
	flushLegacyProvider()
	return strings.Join(out, "\n"), changed
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".codex-cliproxy-gateway-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func validateTOML(label string, data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%s is invalid TOML: %w", label, err)
	}
	return nil
}

func PatchConfig(input string, cfg config.Config) string {
	return patchConfig(input, cfg, true)
}

func patchConfig(input string, cfg config.Config, rewriteLegacyProviders bool) string {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+12)
	inTargetProvider := false
	inLegacyProvider := false
	legacyBaseURLSeen := false
	seenTable := false
	gatewayURL := "http://" + cfg.Listen + "/v1"
	flushLegacyProvider := func() {
		if inLegacyProvider && !legacyBaseURLSeen {
			out = append(out, fmt.Sprintf("base_url = %q", gatewayURL))
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == managedComment {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			flushLegacyProvider()
			seenTable = true
			inTargetProvider = isProviderTable(trimmed, providerID)
			inLegacyProvider = rewriteLegacyProviders && isLegacyProviderTable(trimmed, cfg.LegacyProviderIDs)
			legacyBaseURLSeen = false
			if inTargetProvider {
				continue
			}
		}
		if inTargetProvider {
			continue
		}
		if inLegacyProvider && isTopLevelAssignment(trimmed, "base_url") {
			out = append(out, fmt.Sprintf("base_url = %q", gatewayURL))
			legacyBaseURLSeen = true
			continue
		}
		if !seenTable && (isTopLevelAssignment(trimmed, "model_provider") || isTopLevelAssignment(trimmed, "model_catalog_json")) {
			continue
		}
		if !seenTable {
			if rewritten, ok := rewriteSelectedModel(trimmed, cfg); ok {
				out = append(out, rewritten)
				continue
			}
		}
		out = append(out, line)
	}
	flushLegacyProvider()
	out = collapseBlankLines(out)

	prefix := []string{
		managedComment,
		fmt.Sprintf("model_provider = %q", providerID),
		fmt.Sprintf("model_catalog_json = %q", cfg.ModelCatalogPath),
		"",
	}
	provider := []string{
		"",
		"[model_providers." + providerID + "]",
		"name = \"OpenAI\"",
		fmt.Sprintf("base_url = %q", gatewayURL),
		"wire_api = \"responses\"",
		"requires_openai_auth = true",
		"supports_websockets = false",
		"supports_standalone_web_search = true",
		"",
	}
	patched := append(prefix, out...)
	patched = append(patched, provider...)
	return strings.TrimSpace(strings.Join(patched, "\n")) + "\n"
}

func isLegacyProviderTable(line string, providerIDs []string) bool {
	for _, id := range providerIDs {
		if isProviderTable(line, id) {
			return true
		}
	}
	return false
}

func isProviderTable(line, id string) bool {
	return line == "[model_providers."+id+"]" ||
		line == `[model_providers."`+id+`"]` ||
		line == "[model_providers.'"+id+"']"
}

func collapseBlankLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	previousBlank := true
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && previousBlank {
			continue
		}
		out = append(out, line)
		previousBlank = blank
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func isTopLevelAssignment(line, key string) bool {
	return strings.HasPrefix(line, key) && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(line, key)), "=")
}

func rewriteSelectedModel(line string, cfg config.Config) (string, bool) {
	if !isTopLevelAssignment(line, "model") {
		return "", false
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", false
	}
	selected, err := strconv.Unquote(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", false
	}
	for _, model := range cfg.Models {
		if selected == model.ID {
			return fmt.Sprintf("model = %q", cfg.ModelPrefix+model.ID), true
		}
	}
	return line, true
}

func statePath(cfg config.Config) string {
	return filepath.Join(cfg.CodexHome, "codex-cliproxy-gateway", "install-state.json")
}

func saveState(cfg config.Config, state State) error {
	path := statePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

func loadState(cfg config.Config) (State, error) {
	data, err := os.ReadFile(statePath(cfg))
	if err != nil {
		return State{}, fmt.Errorf("read install state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func managedHash(data []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	seenTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			seenTable = true
		}
		if !seenTable && (isTopLevelAssignment(trimmed, "model") || isTopLevelAssignment(trimmed, "model_reasoning_effort")) {
			continue
		}
		out = append(out, line)
	}
	return hash([]byte(strings.Join(out, "\n")))
}

func matchesInstalledConfig(current []byte, state State, cfg config.Config) bool {
	if state.HashMode == normalizedHashMode {
		return managedHash(current) == state.InstalledHash
	}
	if hash(current) == state.InstalledHash {
		return true
	}
	backup, err := os.ReadFile(state.BackupPath)
	if err != nil {
		return false
	}
	legacyInstalled := patchConfig(string(backup), cfg, false)
	return managedHash(current) == managedHash([]byte(legacyInstalled))
}
