package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultConfigName = "codex-cliproxy-gateway.json"
const CurrentSchemaVersion = 1

const (
	kimiCodeSetupURL       = "https://www.kimi.com/code"
	legacyKimiCodeSetupURL = "https://www.kimi.com/code/console"
	kimiCodeSetupHint      = "Alternatively, keep using a Kimi Code API key by adding it to the local CLIProxyAPI config yourself."
)

type ModelCapabilities struct {
	Streaming     bool `json:"streaming"`
	Tools         bool `json:"tools"`
	ParallelTools bool `json:"parallel_tools"`
	WebSearch     bool `json:"web_search"`
}

type ModelCompatibility struct {
	Status      string `json:"status,omitempty"`
	CodexCLI    string `json:"codex_cli,omitempty"`
	CLIProxyAPI string `json:"cliproxyapi,omitempty"`
}

type ProviderSpec struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	SetupURL      string `json:"setup_url"`
	SetupHint     string `json:"setup_hint,omitempty"`
	RequiredModel string `json:"required_model"`
}

type ModelSpec struct {
	ID                            string             `json:"id"`
	DisplayName                   string             `json:"display_name"`
	Description                   string             `json:"description,omitempty"`
	ProviderID                    string             `json:"provider_id,omitempty"`
	Route                         string             `json:"route,omitempty"`
	UpstreamModel                 string             `json:"upstream_model,omitempty"`
	WireAPI                       string             `json:"wire_api,omitempty"`
	ContextWindow                 int64              `json:"context_window,omitempty"`
	InputModalities               []string           `json:"input_modalities,omitempty"`
	OutputModalities              []string           `json:"output_modalities,omitempty"`
	ReasoningLevels               []string           `json:"reasoning_levels,omitempty"`
	DefaultReasoningLevel         string             `json:"default_reasoning_level,omitempty"`
	ReasoningWireFormat           string             `json:"reasoning_wire_format,omitempty"`
	ExcludedToolNamespacePrefixes []string           `json:"excluded_tool_namespace_prefixes,omitempty"`
	Capabilities                  ModelCapabilities  `json:"capabilities"`
	Compatibility                 ModelCompatibility `json:"compatibility,omitempty"`
}

type Sidecar struct {
	Enabled  bool     `json:"enabled"`
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	ReadyURL string   `json:"ready_url,omitempty"`
}

type Config struct {
	SchemaVersion       int            `json:"schema_version"`
	Listen              string         `json:"listen"`
	AllowNonLoopback    bool           `json:"allow_non_loopback,omitempty"`
	OfficialBaseURL     string         `json:"official_base_url"`
	CLIProxyBaseURL     string         `json:"cliproxy_base_url"`
	CLIProxyAPIKeyEnv   string         `json:"cliproxy_api_key_env"`
	CLIProxyAPIKeyFile  string         `json:"cliproxy_api_key_file"`
	CLIProxyConfigPath  string         `json:"cliproxy_config_path"`
	CLIProxyAuthDir     string         `json:"cliproxy_auth_dir,omitempty"`
	ZstdCommand         string         `json:"zstd_command,omitempty"`
	ModelPrefix         string         `json:"model_prefix"`
	CodexHome           string         `json:"codex_home"`
	OfficialModelsCache string         `json:"official_models_cache"`
	ModelCatalogPath    string         `json:"model_catalog_path"`
	LegacyProviderIDs   []string       `json:"legacy_provider_ids,omitempty"`
	Providers           []ProviderSpec `json:"providers,omitempty"`
	Models              []ModelSpec    `json:"models"`
	Sidecar             Sidecar        `json:"sidecar"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	codexHome := filepath.Join(home, ".codex")
	return Config{
		SchemaVersion:       CurrentSchemaVersion,
		Listen:              "127.0.0.1:8765",
		OfficialBaseURL:     "https://chatgpt.com/backend-api/codex",
		CLIProxyBaseURL:     "http://127.0.0.1:8317/v1",
		CLIProxyAPIKeyEnv:   "CLIPROXY_API_KEY",
		CLIProxyAPIKeyFile:  filepath.Join(codexHome, "codex-cliproxy-gateway", "cliproxy-api-key"),
		CLIProxyConfigPath:  filepath.Join(home, ".cli-proxy-api", "config.yaml"),
		CLIProxyAuthDir:     "",
		ZstdCommand:         "",
		ModelPrefix:         "cliproxy/",
		CodexHome:           codexHome,
		OfficialModelsCache: filepath.Join(codexHome, "models_cache.json"),
		ModelCatalogPath:    filepath.Join(codexHome, "model-catalogs", "codex-cliproxy-gateway.json"),
		LegacyProviderIDs:   []string{"openai-http"},
		Providers: []ProviderSpec{
			{
				ID:            "kimi-code",
				DisplayName:   "Kimi Code",
				SetupURL:      kimiCodeSetupURL,
				SetupHint:     kimiCodeSetupHint,
				RequiredModel: "kimi-k3",
			},
		},
		Models: []ModelSpec{
			{
				ID:                    "kimi-k3",
				DisplayName:           "Kimi K3",
				Description:           "Kimi K3 via Kimi Code and CLIProxyAPI; 1M context requires an eligible membership",
				ProviderID:            "kimi-code",
				Route:                 "cliproxy",
				UpstreamModel:         "kimi-k3",
				WireAPI:               "responses",
				ContextWindow:         1_048_576,
				InputModalities:       []string{"text", "image"},
				OutputModalities:      []string{"text"},
				ReasoningLevels:       []string{"none"},
				DefaultReasoningLevel: "none",
				ReasoningWireFormat:   "kimi-thinking",
				ExcludedToolNamespacePrefixes: []string{
					"mcp__codex_apps__",
				},
				Capabilities: ModelCapabilities{
					Streaming: true,
					Tools:     true,
				},
				Compatibility: ModelCompatibility{
					Status:      "experimental",
					CLIProxyAPI: "7.3.11",
				},
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
	data, err := os.ReadFile(ExpandPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var header struct {
		SchemaVersion *int            `json:"schema_version"`
		Providers     json.RawMessage `json:"providers"`
		Models        json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	legacySchema := header.SchemaVersion == nil || *header.SchemaVersion == 0
	// A JSON model array replaces the defaults. Clear the preloaded slice first
	// so encoding/json cannot reuse a default element and leak Kimi-specific
	// fields into a different legacy model.
	if header.Models != nil {
		cfg.Models = nil
	}
	if header.Providers != nil {
		cfg.Providers = nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("parse config: multiple JSON values are not allowed")
	}
	cfg.applyDefaults(legacySchema)
	cfg.expandPaths()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults(legacySchema bool) {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = CurrentSchemaVersion
	}
	for i := range c.Providers {
		provider := &c.Providers[i]
		if provider.ID != "kimi-code" {
			continue
		}
		if provider.SetupURL == legacyKimiCodeSetupURL {
			provider.SetupURL = kimiCodeSetupURL
		}
		if strings.TrimSpace(provider.SetupHint) == "" {
			provider.SetupHint = kimiCodeSetupHint
		}
	}
	models := c.Models[:0]
	for i := range c.Models {
		model := &c.Models[i]
		if isDeprecatedBundledKimi256K(*model) {
			continue
		}
		if model.Route == "" {
			model.Route = "cliproxy"
		}
		if model.UpstreamModel == "" {
			model.UpstreamModel = model.ID
		}
		if model.WireAPI == "" {
			model.WireAPI = "responses"
		}
		if len(model.OutputModalities) == 0 {
			model.OutputModalities = []string{"text"}
		}
		if len(model.ReasoningLevels) == 0 {
			model.ReasoningLevels = []string{"none"}
		}
		if model.DefaultReasoningLevel == "" {
			model.DefaultReasoningLevel = model.ReasoningLevels[0]
		}
		if model.ReasoningWireFormat == "" && model.ProviderID == "kimi-code" {
			model.ReasoningWireFormat = "kimi-thinking"
		}
		if model.ProviderID == "kimi-code" {
			// Kimi exposes native thinking as a single provider-managed mode, not
			// Codex-style effort tiers. Normalize older generated configs so an
			// existing installation no longer advertises Low/High/Max.
			model.ReasoningLevels = []string{"none"}
			model.DefaultReasoningLevel = "none"
			if model.ExcludedToolNamespacePrefixes == nil {
				model.ExcludedToolNamespacePrefixes = []string{"mcp__codex_apps__"}
			}
		}
		if legacySchema {
			model.Capabilities.Streaming = true
			model.Capabilities.Tools = true
		}
		models = append(models, *model)
	}
	c.Models = models
}

func isDeprecatedBundledKimi256K(model ModelSpec) bool {
	return model.ID == "kimi-k3-256k" &&
		model.ProviderID == "kimi-code" &&
		model.UpstreamModel == "kimi-k3-256k" &&
		model.ContextWindow == 262_144 &&
		model.Compatibility.CLIProxyAPI == "7.3.11"
}

func (c *Config) expandPaths() {
	c.CodexHome = ExpandPath(c.CodexHome)
	c.OfficialModelsCache = ExpandPath(c.OfficialModelsCache)
	c.ModelCatalogPath = ExpandPath(c.ModelCatalogPath)
	c.CLIProxyAPIKeyFile = ExpandPath(c.CLIProxyAPIKeyFile)
	c.CLIProxyConfigPath = ExpandPath(c.CLIProxyConfigPath)
	c.CLIProxyAuthDir = ExpandPath(c.CLIProxyAuthDir)
	c.ZstdCommand = ExpandPath(c.ZstdCommand)
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

func (c Config) ResolveCLIProxyAuthDir() (string, error) {
	if configured := strings.TrimSpace(c.CLIProxyAuthDir); configured != "" {
		return ExpandPath(configured), nil
	}
	file, err := os.Open(c.CLIProxyConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Dir(c.CLIProxyConfigPath), nil
	}
	if err != nil {
		return "", fmt.Errorf("open CLIProxyAPI config to resolve auth-dir: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		key, raw, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "auth-dir" {
			continue
		}
		value, parseErr := parseYAMLScalar(raw)
		if parseErr != nil {
			return "", fmt.Errorf("parse CLIProxyAPI auth-dir: %w", parseErr)
		}
		if strings.TrimSpace(value) == "" {
			return "", errors.New("CLIProxyAPI auth-dir is empty")
		}
		return ExpandPath(value), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read CLIProxyAPI config to resolve auth-dir: %w", err)
	}
	return filepath.Dir(c.CLIProxyConfigPath), nil
}

func parseYAMLScalar(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, `"`) {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", err
		}
		return parsed, nil
	}
	if strings.HasPrefix(value, "'") {
		if len(value) < 2 || !strings.HasSuffix(value, "'") {
			return "", errors.New("unterminated single-quoted value")
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	if before, _, found := strings.Cut(value, " #"); found {
		value = before
	}
	return strings.TrimSpace(value), nil
}

func ExpandPath(path string) string {
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
	if c.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d; this binary supports %d", c.SchemaVersion, CurrentSchemaVersion)
	}
	if c.Listen == "" {
		return errors.New("listen is required")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be host:port: %w", err)
	}
	if !c.AllowNonLoopback && host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("listen address %q is not loopback; set allow_non_loopback only after reviewing the security risk", host)
		}
	}
	if c.OfficialBaseURL == "" || c.CLIProxyBaseURL == "" {
		return errors.New("official_base_url and cliproxy_base_url are required")
	}
	if c.ModelPrefix == "" {
		return errors.New("model_prefix is required")
	}
	if !strings.HasSuffix(c.ModelPrefix, "/") {
		return errors.New("model_prefix must end with /")
	}
	seenProviders := make(map[string]struct{}, len(c.Providers))
	for i, provider := range c.Providers {
		if strings.TrimSpace(provider.ID) == "" {
			return fmt.Errorf("providers[%d].id is required", i)
		}
		if _, exists := seenProviders[provider.ID]; exists {
			return fmt.Errorf("providers[%d].id %q is duplicated", i, provider.ID)
		}
		seenProviders[provider.ID] = struct{}{}
		if strings.TrimSpace(provider.DisplayName) == "" || strings.TrimSpace(provider.RequiredModel) == "" {
			return fmt.Errorf("providers[%d].display_name and required_model are required", i)
		}
		setupURL, errURL := url.Parse(provider.SetupURL)
		if errURL != nil || (setupURL.Scheme != "https" && setupURL.Scheme != "http") || setupURL.Host == "" {
			return fmt.Errorf("providers[%d].setup_url must be an absolute HTTP(S) URL", i)
		}
	}
	seenModels := make(map[string]struct{}, len(c.Models))
	for i, model := range c.Models {
		if strings.TrimSpace(model.ID) == "" {
			return fmt.Errorf("models[%d].id is required", i)
		}
		if strings.HasPrefix(model.ID, c.ModelPrefix) {
			return fmt.Errorf("models[%d].id must not include prefix %q", i, c.ModelPrefix)
		}
		if _, exists := seenModels[model.ID]; exists {
			return fmt.Errorf("models[%d].id %q is duplicated", i, model.ID)
		}
		seenModels[model.ID] = struct{}{}
		if model.ProviderID != "" {
			if _, exists := seenProviders[model.ProviderID]; !exists {
				return fmt.Errorf("models[%d].provider_id %q is not configured", i, model.ProviderID)
			}
		}
		if model.Route != "cliproxy" {
			return fmt.Errorf("models[%d].route %q is unsupported", i, model.Route)
		}
		if strings.TrimSpace(model.UpstreamModel) == "" {
			return fmt.Errorf("models[%d].upstream_model is required", i)
		}
		if model.WireAPI != "responses" {
			return fmt.Errorf("models[%d].wire_api %q is unsupported", i, model.WireAPI)
		}
		if model.ContextWindow <= 0 {
			return fmt.Errorf("models[%d].context_window must be positive", i)
		}
		if len(model.InputModalities) == 0 || len(model.OutputModalities) == 0 {
			return fmt.Errorf("models[%d] input_modalities and output_modalities are required", i)
		}
		if !contains(model.InputModalities, "text") || !contains(model.OutputModalities, "text") {
			return fmt.Errorf("models[%d] must support text input and output", i)
		}
		if !model.Capabilities.Streaming {
			return fmt.Errorf("models[%d].capabilities.streaming must be true for Codex", i)
		}
		if model.Capabilities.ParallelTools && !model.Capabilities.Tools {
			return fmt.Errorf("models[%d].capabilities.parallel_tools requires tools", i)
		}
		if len(model.ReasoningLevels) == 0 {
			return fmt.Errorf("models[%d].reasoning_levels is required", i)
		}
		if model.DefaultReasoningLevel == "" || !contains(model.ReasoningLevels, model.DefaultReasoningLevel) {
			return fmt.Errorf("models[%d].default_reasoning_level must be one of reasoning_levels", i)
		}
		if model.ReasoningWireFormat != "" && model.ReasoningWireFormat != "openai" && model.ReasoningWireFormat != "kimi-thinking" {
			return fmt.Errorf("models[%d].reasoning_wire_format %q is unsupported", i, model.ReasoningWireFormat)
		}
		if model.Compatibility.Status != "" && model.Compatibility.Status != "verified" && model.Compatibility.Status != "experimental" && model.Compatibility.Status != "unsupported" {
			return fmt.Errorf("models[%d].compatibility.status %q is invalid", i, model.Compatibility.Status)
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

func (c Config) Provider(id string) (ProviderSpec, bool) {
	for _, provider := range c.Providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return ProviderSpec{}, false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (c Config) Save(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandPath(path)
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
