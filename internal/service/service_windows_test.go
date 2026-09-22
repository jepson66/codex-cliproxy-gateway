//go:build windows

package service

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestTaskXMLIsValidAndUsesLeastPrivilege(t *testing.T) {
	document := TaskXML(
		"Gateway & helper",
		`C:\Program Files\Gateway\gateway.exe`,
		[]string{"serve", "--config", `C:\Users\Test User\.codex\gateway.json`},
		"S-1-5-21-1000",
	)
	var decoded struct{}
	if err := xml.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("invalid task XML: %v\n%s", err, document)
	}
	for _, expected := range []string{
		"Gateway &amp; helper",
		`<LogonType>InteractiveToken</LogonType>`,
		`<RunLevel>LeastPrivilege</RunLevel>`,
		`<UserId>S-1-5-21-1000</UserId>`,
		`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`<Arguments>serve --config `,
		`C:\Users\Test User\.codex\gateway.json`,
	} {
		if !strings.Contains(document, expected) {
			t.Fatalf("task XML missing %q:\n%s", expected, document)
		}
	}
}

func TestWindowsQuoteArg(t *testing.T) {
	tests := map[string]string{
		"plain":       "plain",
		"two words":   `"two words"`,
		`ends slash\`: `"ends slash\\"`,
		`a"b`:         `"a\"b"`,
	}
	for input, want := range tests {
		if got := windowsQuoteArg(input); got != want {
			t.Errorf("windowsQuoteArg(%q) = %q, want %q", input, got, want)
		}
	}
}
