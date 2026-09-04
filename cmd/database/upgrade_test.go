package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteUpgradeFileSuccess(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "instance.json")
	content := []byte(`{"instance_id":"test-123"}` + "\n")

	if err := writeUpgradeFile(targetPath, content); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	read, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(read) != string(content) {
		t.Fatalf("expected content %q, got %q", string(content), string(read))
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected mode 0600, got %o", perm)
	}
}

func TestWriteUpgradeFileInvalidDirectory(t *testing.T) {
	targetPath := filepath.Join("/nonexistent-dir/sub", "instance.json")
	if err := writeUpgradeFile(targetPath, []byte("data")); err == nil {
		t.Fatalf("expected error for nonexistent directory, got nil")
	}
}
