package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyExecutableStagesBeforeStoppingService(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "bin", "destination")
	if err := os.WriteFile(source, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	callbackCalls := 0
	if err := copyExecutable(source, destination, func() error {
		callbackCalls++
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("destination was replaced before callback: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 1 {
		t.Fatalf("callback calls = %d", callbackCalls)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "new-binary" {
		t.Fatalf("destination = %q, %v", data, err)
	}
}

func TestInspectComponentReportsFilesAndRuntimeState(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Binary:     filepath.Join(dir, "bin", "gateway"),
		Definition: filepath.Join(dir, "services", "gateway.service"),
	}
	for _, path := range []string{paths.Binary, paths.Definition} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	queryCalls := 0
	status, err := inspectComponent("Gateway", Label, paths, func(got Paths, label string) (bool, string, error) {
		queryCalls++
		if got != paths || label != Label {
			t.Fatalf("query = %#v, %q", got, label)
		}
		return true, "running", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if queryCalls != 1 || !status.BinaryPresent || !status.Installed || !status.Running || status.State != "running" {
		t.Fatalf("status = %#v, query calls = %d", status, queryCalls)
	}
}

func TestInspectComponentDoesNotQueryMissingDefinition(t *testing.T) {
	paths := Paths{
		Binary:     filepath.Join(t.TempDir(), "missing-binary"),
		Definition: filepath.Join(t.TempDir(), "missing-definition"),
	}
	status, err := inspectComponent("Gateway", Label, paths, func(Paths, string) (bool, string, error) {
		t.Fatal("query called for missing definition")
		return false, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Installed || status.BinaryPresent || status.Running || status.State != "not-installed" {
		t.Fatalf("status = %#v", status)
	}
}

func TestReportStringIsExplicit(t *testing.T) {
	report := Report{
		Manager: "test manager",
		Gateway: ComponentStatus{
			Name:          "Gateway",
			Binary:        "/bin/gateway",
			Definition:    "/services/gateway",
			BinaryPresent: true,
			Installed:     true,
			Running:       true,
			State:         "running",
		},
		CLIProxy: ComponentStatus{
			Name:       "Managed CLIProxyAPI",
			Binary:     "/bin/cliproxy",
			Definition: "/services/cliproxy",
			State:      "not-installed",
		},
	}
	text := report.String()
	for _, expected := range []string{
		"Service manager: test manager",
		"binary: /bin/gateway (present)",
		"definition: /services/gateway (present)",
		"state: running",
		"running: yes",
		"Managed CLIProxyAPI:",
		"state: not-installed",
		"running: no",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("report missing %q:\n%s", expected, text)
		}
	}
}

func TestCopyExecutableSkipsItsOwnInstalledPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway")
	if err := os.WriteFile(path, []byte("same-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	callbackCalls := 0
	if err := copyExecutable(path, path, func() error {
		callbackCalls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 0 {
		t.Fatalf("callback ran for identical source and destination")
	}
}
