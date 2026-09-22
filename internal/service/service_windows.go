//go:build windows

package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const managerName = "per-user Task Scheduler task"

const gatewayTaskName = "Codex CLIProxy Gateway"
const cliProxyTaskName = "Codex CLIProxy Gateway - CLIProxyAPI"

func defaultGatewayPaths(home string) (Paths, error) {
	root, err := windowsInstallRoot(home)
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Binary:     filepath.Join(root, "bin", "codex-cliproxy-gateway.exe"),
		Definition: filepath.Join(root, "services", "gateway-task.xml"),
		LogDir:     filepath.Join(root, "logs"),
	}, nil
}

func defaultCLIProxyPaths(home string) (Paths, error) {
	root, err := windowsInstallRoot(home)
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Binary:     filepath.Join(root, "bin", "cli-proxy-api.exe"),
		Definition: filepath.Join(root, "services", "cliproxyapi-task.xml"),
		LogDir:     filepath.Join(root, "logs"),
	}, nil
}

func windowsInstallRoot(home string) (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		if home == "" {
			return "", fmt.Errorf("LOCALAPPDATA and user home are unavailable")
		}
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(localAppData, "codex-cliproxy-gateway"), nil
}

func installGatewayService(paths Paths, configPath string) error {
	definition, err := taskXMLForCurrentUser("Codex CLIProxy Gateway", paths.Binary, []string{"serve", "--config", configPath})
	if err != nil {
		return err
	}
	return installTask(gatewayTaskName, paths.Definition, definition)
}

func uninstallGatewayService(paths Paths) error {
	if err := deleteTask(gatewayTaskName); err != nil {
		return err
	}
	return removeDefinition(paths.Definition, "Task Scheduler")
}

func installCLIProxyService(paths Paths, configPath string) error {
	definition, err := taskXMLForCurrentUser("CLIProxyAPI for Codex CLIProxy Gateway", paths.Binary, []string{"-config", configPath})
	if err != nil {
		return err
	}
	return installTask(cliProxyTaskName, paths.Definition, definition)
}

func uninstallCLIProxyService(paths Paths) error {
	if err := deleteTask(cliProxyTaskName); err != nil {
		return err
	}
	return removeDefinition(paths.Definition, "CLIProxyAPI Task Scheduler")
}

func installTask(name, definitionPath, definition string) error {
	if err := writeDefinition(definitionPath, "Task Scheduler", definition); err != nil {
		return err
	}
	_ = exec.Command("schtasks.exe", "/End", "/TN", name).Run()
	if output, err := exec.Command("schtasks.exe", "/Create", "/TN", name, "/XML", definitionPath, "/F").CombinedOutput(); err != nil {
		return fmt.Errorf("register Task Scheduler task %s: %w: %s", name, err, bytes.TrimSpace(output))
	}
	if output, err := exec.Command("schtasks.exe", "/Run", "/TN", name).CombinedOutput(); err != nil {
		return fmt.Errorf("start Task Scheduler task %s: %w: %s", name, err, bytes.TrimSpace(output))
	}
	return nil
}

func stopGatewayForUpdate(paths Paths) error {
	return stopTaskIfInstalled(paths.Definition, gatewayTaskName)
}

func stopCLIProxyForUpdate(paths Paths) error {
	return stopTaskIfInstalled(paths.Definition, cliProxyTaskName)
}

func stopTaskIfInstalled(definition, name string) error {
	if _, err := os.Stat(definition); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	_ = exec.Command("schtasks.exe", "/End", "/TN", name).Run()
	return nil
}

func queryService(_ Paths, label string) (bool, string, error) {
	taskName := gatewayTaskName
	if label == CLIProxyLabel {
		taskName = cliProxyTaskName
	}
	script := fmt.Sprintf("(Get-ScheduledTask -TaskName '%s' -ErrorAction Stop).State.ToString()", strings.ReplaceAll(taskName, "'", "''"))
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return false, "unknown", fmt.Errorf("query Task Scheduler task %s: %w: %s", taskName, err, bytes.TrimSpace(output))
	}
	state := strings.ToLower(strings.TrimSpace(string(output)))
	if state == "" {
		state = "unknown"
	}
	return state == "running", state, nil
}

func deleteTask(name string) error {
	_ = exec.Command("schtasks.exe", "/End", "/TN", name).Run()
	output, err := exec.Command("schtasks.exe", "/Delete", "/TN", name, "/F").CombinedOutput()
	if err != nil {
		lower := bytes.ToLower(output)
		if bytes.Contains(lower, []byte("cannot find")) || bytes.Contains(lower, []byte("does not exist")) {
			return nil
		}
		return fmt.Errorf("delete Task Scheduler task %s: %w: %s", name, err, bytes.TrimSpace(output))
	}
	return nil
}

func taskXMLForCurrentUser(description, binary string, args []string) (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current Windows user: %w", err)
	}
	if current.Uid == "" {
		return "", fmt.Errorf("resolve current Windows user: SID is empty")
	}
	return TaskXML(description, binary, args, current.Uid), nil
}

func TaskXML(description, binary string, args []string, userSID string) string {
	values := map[string]string{
		"description": description,
		"binary":      binary,
		"arguments":   strings.Join(mapWindowsArgs(args), " "),
		"working_dir": filepath.Dir(binary),
		"user_sid":    userSID,
	}
	for key, value := range values {
		values[key] = xmlEscapeWindows(value)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>%s</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`, values["description"], values["user_sid"], values["user_sid"], values["binary"], values["arguments"], values["working_dir"])
}

func mapWindowsArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windowsQuoteArg(arg)
	}
	return quoted
}

func windowsQuoteArg(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for _, char := range value {
		switch char {
		case '\\':
			backslashes++
		case '"':
			result.WriteString(strings.Repeat(`\`, backslashes*2+1))
			result.WriteRune(char)
			backslashes = 0
		default:
			result.WriteString(strings.Repeat(`\`, backslashes))
			result.WriteRune(char)
			backslashes = 0
		}
	}
	result.WriteString(strings.Repeat(`\`, backslashes*2))
	result.WriteByte('"')
	return result.String()
}

func xmlEscapeWindows(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
