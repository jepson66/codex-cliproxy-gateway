package service

import (
	"strings"
	"testing"
)

func TestPlistEscapesPathsAndStartsServe(t *testing.T) {
	paths := Paths{
		Binary: "/tmp/a&b/gateway",
		Plist:  "/tmp/unused.plist",
		LogDir: "/tmp/logs",
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
