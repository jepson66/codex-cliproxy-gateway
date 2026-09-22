//go:build linux

package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const managerName = "systemd user service"

func defaultGatewayPaths(home string) (Paths, error) {
	return Paths{
		Binary:     filepath.Join(home, ".local", "bin", "codex-cliproxy-gateway"),
		Definition: filepath.Join(home, ".config", "systemd", "user", Label+".service"),
		LogDir:     filepath.Join(home, ".codex", "codex-cliproxy-gateway"),
	}, nil
}

func defaultCLIProxyPaths(home string) (Paths, error) {
	return Paths{
		Binary:     filepath.Join(home, ".local", "bin", "cli-proxy-api"),
		Definition: filepath.Join(home, ".config", "systemd", "user", CLIProxyLabel+".service"),
		LogDir:     filepath.Join(home, ".codex", "codex-cliproxy-gateway"),
	}, nil
}

func installGatewayService(paths Paths, configPath string) error {
	if err := writeDefinition(paths.Definition, "systemd user service", GatewayUnit(paths, configPath)); err != nil {
		return err
	}
	return enableSystemdUnit(Label + ".service")
}

func uninstallGatewayService(paths Paths) error {
	if _, err := os.Stat(paths.Definition); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := disableSystemdUnitIfInstalled(paths.Definition, Label+".service"); err != nil {
		return err
	}
	if err := removeDefinition(paths.Definition, "systemd user service"); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

func installCLIProxyService(paths Paths, configPath string) error {
	if err := writeDefinition(paths.Definition, "CLIProxyAPI systemd user service", CLIProxyUnit(paths, configPath)); err != nil {
		return err
	}
	return enableSystemdUnit(CLIProxyLabel + ".service")
}

func uninstallCLIProxyService(paths Paths) error {
	if _, err := os.Stat(paths.Definition); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := disableSystemdUnitIfInstalled(paths.Definition, CLIProxyLabel+".service"); err != nil {
		return err
	}
	if err := removeDefinition(paths.Definition, "CLIProxyAPI systemd user service"); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

func enableSystemdUnit(name string) error {
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := systemctl("enable", name); err != nil {
		return err
	}
	return systemctl("restart", name)
}

func disableSystemdUnitIfInstalled(definition, name string) error {
	if _, err := os.Stat(definition); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	output, err := exec.Command("systemctl", "--user", "disable", "--now", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("disable systemd user service %s: %w: %s", name, err, bytes.TrimSpace(output))
	}
	return nil
}

func stopGatewayForUpdate(paths Paths) error {
	return stopSystemdUnitIfInstalled(paths.Definition, Label+".service")
}

func stopCLIProxyForUpdate(paths Paths) error {
	return stopSystemdUnitIfInstalled(paths.Definition, CLIProxyLabel+".service")
}

func stopSystemdUnitIfInstalled(definition, name string) error {
	if _, err := os.Stat(definition); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return systemctl("stop", name)
}

func queryService(_ Paths, label string) (bool, string, error) {
	name := label + ".service"
	output, err := exec.Command("systemctl", "--user", "is-active", name).CombinedOutput()
	state := strings.TrimSpace(string(output))
	if err == nil {
		return state == "active", state, nil
	}
	if _, ok := err.(*exec.ExitError); ok && state != "" {
		return false, state, nil
	}
	return false, "unknown", err
}

func systemctl(args ...string) error {
	commandArgs := append([]string{"--user"}, args...)
	output, err := exec.Command("systemctl", commandArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(output))
	}
	return nil
}

func GatewayUnit(paths Paths, configPath string) string {
	return systemdUnit("Codex CLIProxy Gateway", paths.Binary, []string{"serve", "--config", configPath})
}

func CLIProxyUnit(paths Paths, configPath string) string {
	return systemdUnit("CLIProxyAPI for Codex CLIProxy Gateway", paths.Binary, []string{"-config", configPath})
}

func systemdUnit(description, binary string, args []string) string {
	command := []string{systemdQuote(binary)}
	for _, arg := range args {
		command = append(command, systemdQuote(arg))
	}
	return fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, description, strings.Join(command, " "))
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
