package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const Label = "com.codex-cliproxy-gateway"
const CLIProxyLabel = "com.codex-cliproxy-gateway.cliproxyapi"

type Paths struct {
	Binary string
	Plist  string
	LogDir string
}

type CLIProxyPaths struct {
	Binary string
	Plist  string
	LogDir string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Binary: filepath.Join(home, ".local", "bin", "codex-cliproxy-gateway"),
		Plist:  filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
		LogDir: filepath.Join(home, ".codex", "codex-cliproxy-gateway"),
	}, nil
}

func DefaultCLIProxyPaths() (CLIProxyPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return CLIProxyPaths{}, err
	}
	return CLIProxyPaths{
		Binary: filepath.Join(home, ".local", "bin", "cli-proxy-api"),
		Plist:  filepath.Join(home, "Library", "LaunchAgents", CLIProxyLabel+".plist"),
		LogDir: filepath.Join(home, ".codex", "codex-cliproxy-gateway"),
	}, nil
}

func Install(configPath string) (Paths, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return Paths{}, err
	}
	source, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("find current executable: %w", err)
	}
	if err := copyExecutable(source, paths.Binary); err != nil {
		return Paths{}, err
	}
	if err := os.MkdirAll(filepath.Dir(paths.Plist), 0o700); err != nil {
		return Paths{}, err
	}
	if err := os.MkdirAll(paths.LogDir, 0o700); err != nil {
		return Paths{}, err
	}
	plist := Plist(paths, configPath)
	if err := os.WriteFile(paths.Plist, []byte(plist), 0o600); err != nil {
		return Paths{}, fmt.Errorf("write LaunchAgent: %w", err)
	}

	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain, paths.Plist).Run()
	if output, err := exec.Command("/bin/launchctl", "bootstrap", domain, paths.Plist).CombinedOutput(); err != nil {
		return Paths{}, fmt.Errorf("load LaunchAgent: %w: %s", err, bytes.TrimSpace(output))
	}
	if output, err := exec.Command("/bin/launchctl", "kickstart", "-k", domain+"/"+Label).CombinedOutput(); err != nil {
		return Paths{}, fmt.Errorf("start LaunchAgent: %w: %s", err, bytes.TrimSpace(output))
	}
	return paths, nil
}

func Uninstall() error {
	paths, err := DefaultPaths()
	if err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain, paths.Plist).Run()
	if err := os.Remove(paths.Plist); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove LaunchAgent: %w", err)
	}
	return nil
}

func InstallCLIProxy(binaryPath, configPath string) (CLIProxyPaths, error) {
	paths, err := DefaultCLIProxyPaths()
	if err != nil {
		return CLIProxyPaths{}, err
	}
	if binaryPath != "" {
		paths.Binary = binaryPath
	}
	if info, err := os.Stat(paths.Binary); err != nil || info.IsDir() {
		return CLIProxyPaths{}, fmt.Errorf("CLIProxyAPI binary is unavailable at %s", paths.Binary)
	}
	if info, err := os.Stat(configPath); err != nil || info.IsDir() {
		return CLIProxyPaths{}, fmt.Errorf("CLIProxyAPI config is unavailable at %s", configPath)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Plist), 0o700); err != nil {
		return CLIProxyPaths{}, err
	}
	if err := os.MkdirAll(paths.LogDir, 0o700); err != nil {
		return CLIProxyPaths{}, err
	}
	if err := os.WriteFile(paths.Plist, []byte(CLIProxyPlist(paths, configPath)), 0o600); err != nil {
		return CLIProxyPaths{}, fmt.Errorf("write CLIProxyAPI LaunchAgent: %w", err)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain, paths.Plist).Run()
	if output, err := exec.Command("/bin/launchctl", "bootstrap", domain, paths.Plist).CombinedOutput(); err != nil {
		return CLIProxyPaths{}, fmt.Errorf("load CLIProxyAPI LaunchAgent: %w: %s", err, bytes.TrimSpace(output))
	}
	if output, err := exec.Command("/bin/launchctl", "kickstart", "-k", domain+"/"+CLIProxyLabel).CombinedOutput(); err != nil {
		return CLIProxyPaths{}, fmt.Errorf("start CLIProxyAPI LaunchAgent: %w: %s", err, bytes.TrimSpace(output))
	}
	return paths, nil
}

func UninstallCLIProxy() error {
	paths, err := DefaultCLIProxyPaths()
	if err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain, paths.Plist).Run()
	if err := os.Remove(paths.Plist); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove CLIProxyAPI LaunchAgent: %w", err)
	}
	return nil
}

func Plist(paths Paths, configPath string) string {
	values := map[string]string{
		"label":  Label,
		"binary": paths.Binary,
		"config": configPath,
		"stdout": filepath.Join(paths.LogDir, "gateway.out.log"),
		"stderr": filepath.Join(paths.LogDir, "gateway.err.log"),
	}
	for key, value := range values {
		values[key] = xmlEscape(value)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
    <string>--config</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, values["label"], values["binary"], values["config"], values["stdout"], values["stderr"])
}

func CLIProxyPlist(paths CLIProxyPaths, configPath string) string {
	values := map[string]string{
		"label":  CLIProxyLabel,
		"binary": paths.Binary,
		"config": configPath,
		"stdout": filepath.Join(paths.LogDir, "cliproxyapi.out.log"),
		"stderr": filepath.Join(paths.LogDir, "cliproxyapi.err.log"),
	}
	for key, value := range values {
		values[key] = xmlEscape(value)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-config</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, values["label"], values["binary"], values["config"], values["stdout"], values["stderr"])
}

func copyExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open executable: %w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".codex-cliproxy-gateway-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("install executable: %w", err)
	}
	return nil
}

func xmlEscape(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
