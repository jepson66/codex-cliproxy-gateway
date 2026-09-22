package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const Label = "com.codex-cliproxy-gateway"
const CLIProxyLabel = "com.codex-cliproxy-gateway.cliproxyapi"

// Paths contains the platform-specific locations used to install one service.
// Definition is a launchd plist, systemd unit, or Task Scheduler XML file.
type Paths struct {
	Binary     string
	Definition string
	LogDir     string
}

type CLIProxyPaths = Paths

type ComponentStatus struct {
	Name          string
	Binary        string
	Definition    string
	BinaryPresent bool
	Installed     bool
	Running       bool
	State         string
}

type Report struct {
	Manager  string
	Gateway  ComponentStatus
	CLIProxy ComponentStatus
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return defaultGatewayPaths(home)
}

func DefaultCLIProxyPaths() (CLIProxyPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return CLIProxyPaths{}, err
	}
	return defaultCLIProxyPaths(home)
}

// ManagerName identifies the native per-user service manager used by this
// build. It is intended for installation plans and user-facing status text.
func ManagerName() string {
	return managerName
}

func Inspect() (Report, error) {
	gatewayPaths, err := DefaultPaths()
	if err != nil {
		return Report{}, err
	}
	cliProxyPaths, err := DefaultCLIProxyPaths()
	if err != nil {
		return Report{}, err
	}
	gateway, err := inspectComponent("Gateway", Label, gatewayPaths, queryService)
	if err != nil {
		return Report{}, err
	}
	cliProxy, err := inspectComponent("Managed CLIProxyAPI", CLIProxyLabel, cliProxyPaths, queryService)
	if err != nil {
		return Report{}, err
	}
	return Report{Manager: ManagerName(), Gateway: gateway, CLIProxy: cliProxy}, nil
}

func (r Report) String() string {
	var output strings.Builder
	fmt.Fprintf(&output, "Service manager: %s\n", r.Manager)
	writeComponentStatus(&output, r.Gateway)
	writeComponentStatus(&output, r.CLIProxy)
	return strings.TrimSuffix(output.String(), "\n")
}

func writeComponentStatus(output *strings.Builder, status ComponentStatus) {
	fmt.Fprintf(output, "%s:\n", status.Name)
	fmt.Fprintf(output, "  binary: %s (%s)\n", status.Binary, presentAbsent(status.BinaryPresent))
	fmt.Fprintf(output, "  definition: %s (%s)\n", status.Definition, presentAbsent(status.Installed))
	fmt.Fprintf(output, "  state: %s\n", status.State)
	fmt.Fprintf(output, "  running: %s\n", yesNo(status.Running))
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func presentAbsent(value bool) string {
	if value {
		return "present"
	}
	return "absent"
}

type serviceQuery func(Paths, string) (bool, string, error)

func inspectComponent(name, label string, paths Paths, query serviceQuery) (ComponentStatus, error) {
	status := ComponentStatus{
		Name:       name,
		Binary:     paths.Binary,
		Definition: paths.Definition,
		State:      "not-installed",
	}
	binaryPresent, err := regularFileExists(paths.Binary)
	if err != nil {
		return ComponentStatus{}, fmt.Errorf("inspect %s binary: %w", name, err)
	}
	status.BinaryPresent = binaryPresent
	installed, err := regularFileExists(paths.Definition)
	if err != nil {
		return ComponentStatus{}, fmt.Errorf("inspect %s definition: %w", name, err)
	}
	status.Installed = installed
	if !installed {
		return status, nil
	}
	status.Running, status.State, err = query(paths, label)
	if err != nil {
		return ComponentStatus{}, fmt.Errorf("inspect %s service: %w", name, err)
	}
	return status, nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory", path)
	}
	return true, nil
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
	if err := copyExecutable(source, paths.Binary, func() error { return stopGatewayForUpdate(paths) }); err != nil {
		return Paths{}, err
	}
	if err := preparePaths(paths); err != nil {
		return Paths{}, err
	}
	if err := installGatewayService(paths, configPath); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

func Uninstall() error {
	paths, err := DefaultPaths()
	if err != nil {
		return err
	}
	return uninstallGatewayService(paths)
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
	if err := preparePaths(paths); err != nil {
		return CLIProxyPaths{}, err
	}
	if err := installCLIProxyService(paths, configPath); err != nil {
		return CLIProxyPaths{}, err
	}
	return paths, nil
}

func UninstallCLIProxy() error {
	paths, err := DefaultCLIProxyPaths()
	if err != nil {
		return err
	}
	return uninstallCLIProxyService(paths)
}

// StopCLIProxyForUpdate stops an existing managed CLIProxyAPI process just
// before its verified replacement is written. It does not remove its service
// definition and is a no-op on a clean installation.
func StopCLIProxyForUpdate() error {
	paths, err := DefaultCLIProxyPaths()
	if err != nil {
		return err
	}
	return stopCLIProxyForUpdate(paths)
}

func preparePaths(paths Paths) error {
	if err := os.MkdirAll(filepath.Dir(paths.Definition), 0o700); err != nil {
		return fmt.Errorf("create service definition directory: %w", err)
	}
	if err := os.MkdirAll(paths.LogDir, 0o700); err != nil {
		return fmt.Errorf("create service log directory: %w", err)
	}
	return nil
}

func copyExecutable(source, destination string, beforeWrite func() error) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open executable: %w", err)
	}
	defer input.Close()
	if sourceInfo, sourceErr := input.Stat(); sourceErr == nil {
		if destinationInfo, destinationErr := os.Stat(destination); destinationErr == nil && os.SameFile(sourceInfo, destinationInfo) {
			return nil
		}
	}
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
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return fmt.Errorf("prepare executable update: %w", err)
		}
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("install executable: %w", err)
	}
	return nil
}

func writeDefinition(path, kind, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s definition: %w", kind, err)
	}
	return nil
}

func removeDefinition(path, kind string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s definition: %w", kind, err)
	}
	return nil
}
