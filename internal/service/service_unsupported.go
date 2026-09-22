//go:build !darwin && !linux && !windows

package service

import "fmt"

const managerName = "unsupported service manager"

func unsupportedPlatform() error {
	return fmt.Errorf("per-user service management is unsupported on this operating system")
}

func defaultGatewayPaths(string) (Paths, error)  { return Paths{}, unsupportedPlatform() }
func defaultCLIProxyPaths(string) (Paths, error) { return Paths{}, unsupportedPlatform() }
func installGatewayService(Paths, string) error  { return unsupportedPlatform() }
func uninstallGatewayService(Paths) error        { return unsupportedPlatform() }
func installCLIProxyService(Paths, string) error { return unsupportedPlatform() }
func uninstallCLIProxyService(Paths) error       { return unsupportedPlatform() }
func stopGatewayForUpdate(Paths) error           { return unsupportedPlatform() }
func stopCLIProxyForUpdate(Paths) error          { return unsupportedPlatform() }
