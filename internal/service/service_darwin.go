//go:build darwin

package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const managerName = "per-user LaunchAgent"

func defaultGatewayPaths(home string) (Paths, error) {
	return Paths{
		Binary:     filepath.Join(home, ".local", "bin", "codex-cliproxy-gateway"),
		Definition: filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
		LogDir:     filepath.Join(home, ".codex", "codex-cliproxy-gateway"),
	}, nil
}

func defaultCLIProxyPaths(home string) (Paths, error) {
	return Paths{
		Binary:     filepath.Join(home, ".local", "bin", "cli-proxy-api"),
		Definition: filepath.Join(home, "Library", "LaunchAgents", CLIProxyLabel+".plist"),
		LogDir:     filepath.Join(home, ".codex", "codex-cliproxy-gateway"),
	}, nil
}

func installGatewayService(paths Paths, configPath string) error {
	if err := writeDefinition(paths.Definition, "LaunchAgent", Plist(paths, configPath)); err != nil {
		return err
	}
	return loadLaunchAgent(Label, paths.Definition)
}

func uninstallGatewayService(paths Paths) error {
	unloadLaunchAgent(paths.Definition)
	return removeDefinition(paths.Definition, "LaunchAgent")
}

func installCLIProxyService(paths Paths, configPath string) error {
	if err := writeDefinition(paths.Definition, "CLIProxyAPI LaunchAgent", CLIProxyPlist(paths, configPath)); err != nil {
		return err
	}
	return loadLaunchAgent(CLIProxyLabel, paths.Definition)
}

func uninstallCLIProxyService(paths Paths) error {
	unloadLaunchAgent(paths.Definition)
	return removeDefinition(paths.Definition, "CLIProxyAPI LaunchAgent")
}

func loadLaunchAgent(label, definition string) error {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain, definition).Run()
	if output, err := exec.Command("/bin/launchctl", "bootstrap", domain, definition).CombinedOutput(); err != nil {
		return fmt.Errorf("load LaunchAgent: %w: %s", err, bytes.TrimSpace(output))
	}
	if output, err := exec.Command("/bin/launchctl", "kickstart", "-k", domain+"/"+label).CombinedOutput(); err != nil {
		return fmt.Errorf("start LaunchAgent: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func unloadLaunchAgent(definition string) {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain, definition).Run()
}

func stopGatewayForUpdate(paths Paths) error {
	unloadLaunchAgent(paths.Definition)
	return nil
}

func stopCLIProxyForUpdate(paths Paths) error {
	unloadLaunchAgent(paths.Definition)
	return nil
}

func queryService(_ Paths, label string) (bool, string, error) {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	output, err := exec.Command("/bin/launchctl", "print", domain+"/"+label).CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return false, "not-loaded", nil
		}
		return false, "unknown", err
	}
	state := "loaded"
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("state = ")) {
			state = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("state = "))))
			break
		}
	}
	return state == "running", state, nil
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

func CLIProxyPlist(paths Paths, configPath string) string {
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

func xmlEscape(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
