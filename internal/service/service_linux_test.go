//go:build linux

package service

import (
	"strings"
	"testing"
)

func TestGatewayUnitQuotesPathsAndEscapesSpecifiers(t *testing.T) {
	paths := Paths{Binary: "/tmp/a path/gateway%name"}
	unit := GatewayUnit(paths, `/tmp/config "quoted".json`)
	for _, expected := range []string{
		`ExecStart="/tmp/a path/gateway%%name" "serve" "--config" "/tmp/config \"quoted\".json"`,
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
}

func TestCLIProxyUnitUsesIndependentCommand(t *testing.T) {
	unit := CLIProxyUnit(Paths{Binary: "/tmp/cli-proxy-api"}, "/tmp/config.yaml")
	if !strings.Contains(unit, `ExecStart="/tmp/cli-proxy-api" "-config" "/tmp/config.yaml"`) {
		t.Fatalf("unexpected unit:\n%s", unit)
	}
}
