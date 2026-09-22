package service

import (
	"os"
	"path/filepath"
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
