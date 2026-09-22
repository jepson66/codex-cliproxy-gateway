//go:build darwin

package service

import (
	"strings"
	"testing"
)

func TestPlistEscapesPathsAndStartsServe(t *testing.T) {
	paths := Paths{
		Binary:     "/tmp/a&b/gateway",
		Definition: "/tmp/unused.plist",
		LogDir:     "/tmp/logs",
	}
	plist := Plist(paths, "/tmp/config<test>.json")
	for _, expected := range []string{
		"<string>/tmp/a&amp;b/gateway</string>",
		"<string>serve</string>",
		"<string>--config</string>",
		"<string>/tmp/config&lt;test&gt;.json</string>",
		"<key>KeepAlive</key><true/>",
	} {
		if !strings.Contains(plist, expected) {
			t.Fatalf("plist missing %q:\n%s", expected, plist)
		}
	}
}

func TestCLIProxyPlistUsesIndependentService(t *testing.T) {
	paths := CLIProxyPaths{
		Binary:     "/tmp/cli&proxy",
		Definition: "/tmp/unused.plist",
		LogDir:     "/tmp/logs",
	}
	plist := CLIProxyPlist(paths, "/tmp/config<test>.yaml")
	for _, expected := range []string{
		"<string>com.codex-cliproxy-gateway.cliproxyapi</string>",
		"<string>/tmp/cli&amp;proxy</string>",
		"<string>-config</string>",
		"<string>/tmp/config&lt;test&gt;.yaml</string>",
		"cliproxyapi.err.log",
	} {
		if !strings.Contains(plist, expected) {
			t.Fatalf("plist missing %q:\n%s", expected, plist)
		}
	}
}
