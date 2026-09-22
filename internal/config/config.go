package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const DefaultConfigName = "codex-cliproxy-gateway.json"

type Model struct {
	ID                    string   `json:"id"`
	DisplayName           string   `json:"display_name"`
	Description           string   `json:"description,omitempty"`
	ContextWindow         int64    `json:"context_window,omitempty"`
	InputModalities       []string `json:"input_modalities,omitempty"`
	ReasoningLevels       []string `json:"reasoning_levels,omitempty"`
	DefaultReasoningLevel string   `json:"default_reasoning_level,omitempty"`
}

type Sidecar struct {
	Enabled  bool     `json:"enabled"`
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	ReadyURL string   `json:"ready_url,omitempty"`
}

type Config struct {
	Listen              string   `json:"listen"`
	OfficialBaseURL     string   `json:"official_base_url"`
	CLIProxyBaseURL     string   `json:"cliproxy_base_url"`
	CLIProxyAPIKeyEnv   string   `json:"cliproxy_api_key_env"`
	CLIProxyAPIKeyFile  string   `json:"cliproxy_api_key_file"`
	CLIProxyConfigPath  string   `json:"cliproxy_config_path"`
	ZstdCommand         string   `json:"zstd_command,omitempty"`
	ModelPrefix         string   `json:"model_prefix"`
	CodexHome           string   `json:"codex_home"`
	OfficialModelsCache string   `json:"official_models_cache"`
	ModelCatalogPath    string   `json:"model_catalog_path"`
	LegacyProviderIDs   []string `json:"legacy_provider_ids,omitempty"`
	Models              []Model  `json:"models"`
	Sidecar             Sidecar  `json:"sidecar"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	codexHome := filepath.Join(home, ".codex")
	return Config{
		Listen:              "127.0.0.1:8765",
		OfficialBaseURL:     "https://chatgpt.com/backend-api/codex",
		CLIProxyBaseURL:     "http://127.0.0.1:8317/v1",
		CLIProxyAPIKeyEnv:   "CLIPROXY_API_KEY",
		CLIProxyAPIKeyFile:  filepath.Join(codexHome, "codex-cliproxy-gateway", "cliproxy-api-key"),
		CLIProxyConfigPath:  filepath.Join(home, ".cli-proxy-api", "config.yaml"),
		ZstdCommand:         "",
		ModelPrefix:         "cliproxy/",
		CodexHome:           codexHome,
		OfficialModelsCache: filepath.Join(codexHome, "models_cache.json"),
		ModelCatalogPath:    filepath.Join(codexHome, "model-catalogs", "codex-cliproxy-gateway.json"),
		LegacyProviderIDs:   []string{"openai-http"},
		Models: []Model{
			{
				ID:                    "kimi-k3",
				DisplayName:           "Kimi K3",
				Description:           "Kimi K3 via CLIProxyAPI",
				ContextWindow:         1_048_576,
				InputModalities:       []string{"text", "image"},
				ReasoningLevels:       []string{"none"},
				DefaultReasoningLevel: "none",
			},
		},
		Sidecar: Sidecar{
			Enabled:  false,
			ReadyURL: "http://127.0.0.1:8317/v1/models",
		},
	}
}

func DefaultPath() string {
	return filepath.Join(Default().CodexHome, DefaultConfigName)
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = DefaultPath()
	}
	data, err := os.ReadFile(expandHome(path))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	cfg.expandPaths()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) expandPaths() {
	c.CodexHome = expandHome(c.CodexHome)
	c.OfficialModelsCache = expandHome(c.OfficialModelsCache)
	c.ModelCatalogPath = expandHome(c.ModelCatalogPath)
	c.CLIProxyAPIKeyFile = expandHome(c.CLIProxyAPIKeyFile)
	c.CLIProxyConfigPath = expandHome(c.CLIProxyConfigPath)
	c.ZstdCommand = expandHome(c.ZstdCommand)
}

func (c Config) ResolveZstdCommand() (string, error) {
	if c.ZstdCommand != "" {
		if info, err := os.Stat(c.ZstdCommand); err == nil && !info.IsDir() {
			return c.ZstdCommand, nil
		}
		return "", fmt.Errorf("configured zstd command is not executable: %s", c.ZstdCommand)
	}
	if path, err := exec.LookPath("zstd"); err == nil {
		return path, nil
	}
	for _, path := range []string{"/opt/homebrew/bin/zstd", "/usr/local/bin/zstd"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("zstd command not found; install zstd or set zstd_command")
}

func (c Config) ResolveCLIProxyAPIKey() (string, error) {
	if c.CLIProxyAPIKeyEnv != "" {
		if key := strings.TrimSpace(os.Getenv(c.CLIProxyAPIKeyEnv)); key != "" {
			return key, nil
		}
	}
	if c.CLIProxyAPIKeyFile == "" {
		return "", errors.New("no CLIProxyAPI key environment variable or file is configured")
	}
	data, err := os.ReadFile(c.CLIProxyAPIKeyFile)
	if err != nil {
		return "", fmt.Errorf("read CLIProxyAPI key file: %w", err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("CLIProxyAPI key file is empty")
	}
	return key, nil
}

func (c Config) ImportCLIProxyAPIKey() error {
	file, err := os.Open(c.CLIProxyConfigPath)
	if err != nil {
		return fmt.Errorf("open CLIProxyAPI config: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	inAPIKeys := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !inAPIKeys {
			if trimmed == "api-keys:" {
				inAPIKeys = true
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			break
		}
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		key := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		key = strings.Trim(key, "\"'")
		if key == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(c.CLIProxyAPIKeyFile), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(c.CLIProxyAPIKeyFile, []byte(key+"\n"), 0o600); err != nil {
			return fmt.Errorf("write CLIProxyAPI key file: %w", err)
		}
		return nil
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read CLIProxyAPI config: %w", err)
	}
	return errors.New("CLIProxyAPI config has no scalar api-keys entry")
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen is required")
	}
	if c.OfficialBaseURL == "" || c.CLIProxyBaseURL == "" {
		return errors.New("official_base_url and cliproxy_base_url are required")
	}
	if c.ModelPrefix == "" {
		return errors.New("model_prefix is required")
	}
	for i, model := range c.Models {
		if strings.TrimSpace(model.ID) == "" {
			return fmt.Errorf("models[%d].id is required", i)
		}
		if strings.HasPrefix(model.ID, c.ModelPrefix) {
			return fmt.Errorf("models[%d].id must not include prefix %q", i, c.ModelPrefix)
		}
	}
	for i, providerID := range c.LegacyProviderIDs {
		if strings.TrimSpace(providerID) == "" {
			return fmt.Errorf("legacy_provider_ids[%d] must not be empty", i)
		}
	}
	if c.Sidecar.Enabled && c.Sidecar.Command == "" {
		return errors.New("sidecar.command is required when sidecar.enabled is true")
	}
	return nil
}

func (c Config) Save(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	path = expandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
