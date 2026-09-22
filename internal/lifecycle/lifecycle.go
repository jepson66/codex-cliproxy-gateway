package lifecycle

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex-cliproxy-gateway/internal/config"
	"codex-cliproxy-gateway/internal/lifecycle/dependency"
	"codex-cliproxy-gateway/internal/service"
)

type Mode string

const (
	External Mode = "external"
	Managed  Mode = "managed"
)

type Action struct {
	Operation string
	Target    string
	Details   string
}

type Plan struct {
	Mode    Mode
	Actions []Action
}

func BuildPlan(cfg config.Config, configPath string, mode Mode) (Plan, error) {
	if mode != External && mode != Managed {
		return Plan{}, fmt.Errorf("cliproxy mode must be external or managed")
	}
	gatewayPaths, err := service.DefaultPaths()
	if err != nil {
		return Plan{}, err
	}
	actions := []Action{
		{Operation: createOrPreserve(configPath), Target: configPath, Details: "Gateway JSON config; existing file is preserved"},
		{Operation: "create/update", Target: cfg.ModelCatalogPath, Details: "merged official and allowlisted third-party model catalog"},
		{Operation: "modify selected fields", Target: filepath.Join(cfg.CodexHome, "config.toml"), Details: "preserves unrelated settings; creates a private backup before the first change"},
		{Operation: "install/update", Target: gatewayPaths.Binary, Details: "Gateway executable"},
		{Operation: "install/update", Target: gatewayPaths.Definition, Details: "Gateway " + service.ManagerName()},
	}
	if mode == External {
		actions = append(actions, Action{Operation: "preserve", Target: cfg.CLIProxyConfigPath, Details: "CLIProxyAPI installation, config, and service remain user-managed"})
	} else {
		asset, err := dependency.CurrentAsset(dependency.DefaultCLIProxyAPIVersion)
		if err != nil {
			return Plan{}, err
		}
		cliproxyPaths, err := service.DefaultCLIProxyPaths()
		if err != nil {
			return Plan{}, err
		}
		actions = append(actions,
			Action{Operation: "download/verify/install", Target: cliproxyPaths.Binary, Details: fmt.Sprintf("CLIProxyAPI %s from %s (SHA-256 pinned)", asset.Version, asset.URL)},
			Action{Operation: createOrPreserve(cfg.CLIProxyConfigPath), Target: cfg.CLIProxyConfigPath, Details: "private loopback CLIProxyAPI config; existing provider credentials are never overwritten"},
			Action{Operation: "create/update", Target: cliproxyPaths.Definition, Details: "independent CLIProxyAPI " + service.ManagerName()},
		)
	}
	return Plan{Mode: mode, Actions: actions}, nil
}

func (p Plan) String() string {
	var output strings.Builder
	fmt.Fprintf(&output, "Installation plan (CLIProxyAPI mode: %s):\n", p.Mode)
	for _, action := range p.Actions {
		fmt.Fprintf(&output, "  %-24s %s\n", action.Operation, action.Target)
		fmt.Fprintf(&output, "    %s\n", action.Details)
	}
	output.WriteString("  full replacement of ~/.codex/config.toml: no\n")
	output.WriteString("  provider API keys printed or stored in Git: no")
	return output.String()
}

func EnsureGatewayConfig(path string, cfg config.Config) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	return true, cfg.Save(path)
}

func EnsureManagedCLIProxyConfig(cfg config.Config) (bool, error) {
	if _, err := os.Stat(cfg.CLIProxyConfigPath); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	key, err := randomKey()
	if err != nil {
		return false, err
	}
	authDir := filepath.Dir(cfg.CLIProxyConfigPath)
	content := fmt.Sprintf("host: 127.0.0.1\nport: 8317\nauth-dir: %q\napi-keys:\n  - %q\nopenai-compatibility: []\n", authDir, key)
	if err := writePrivateAtomic(cfg.CLIProxyConfigPath, []byte(content), 0o600); err != nil {
		return false, fmt.Errorf("create CLIProxyAPI config: %w", err)
	}
	if err := writePrivateAtomic(cfg.CLIProxyAPIKeyFile, []byte(key+"\n"), 0o600); err != nil {
		return false, fmt.Errorf("create Gateway CLIProxyAPI key: %w", err)
	}
	return true, nil
}

func createOrPreserve(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "preserve"
	}
	return "create"
}

func randomKey() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "cpg_" + base64.RawURLEncoding.EncodeToString(value), nil
}

func writePrivateAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".codex-cliproxy-gateway-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
