package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRestoreWorkflowCreatesAndRestoresCompleteArtifact(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "nested", "record.txt"), "preserved")
	archive := filepath.Join(t.TempDir(), "instance.tar")

	assertOperatorSuccess(t, []string{"backup", "create", "--data-dir", source, "--output", archive}, "backup create")
	assertOperatorSuccess(t, []string{"backup", "inspect", "--input", archive}, "backup inspect")

	destination := filepath.Join(t.TempDir(), "restored")
	assertOperatorSuccess(t, []string{"restore", "--input", archive, "--data-dir", destination}, "restore")
	contents, err := os.ReadFile(filepath.Join(destination, "nested", "record.txt"))
	if err != nil {
		t.Fatalf("read restored artifact: %v", err)
	}
	if got := string(contents); got != "preserved" {
		t.Fatalf("restored contents = %q, want preserved", got)
	}
}

func TestRestoreRejectsNonEmptyDestination(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "preserved")
	archive := filepath.Join(t.TempDir(), "instance.tar")
	assertOperatorSuccess(t, []string{"backup", "create", "--data-dir", source, "--output", archive}, "backup create")

	destination := t.TempDir()
	writeBackupFixture(t, filepath.Join(destination, "existing.txt"), "existing")
	assertOperatorFailure(t, []string{"restore", "--input", archive, "--data-dir", destination}, "restore", "operation_failed", 1)
}

func TestBackupInspectRejectsInvalidArtifact(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "invalid.tar")
	writeBackupFixture(t, archive, "not a tar archive")

	assertOperatorFailure(t, []string{"backup", "inspect", "--input", archive}, "backup inspect", "operation_failed", 1)
}

func assertOperatorSuccess(t *testing.T, args []string, operation string) {
	t.Helper()
	result, code := operatorResult(t, args)
	if code != 0 {
		t.Fatalf("%v exit code = %d, result = %#v", args, code, result)
	}
	if result["operation"] != operation || result["success"] != true || result["exit_class"] != "success" {
		t.Fatalf("%v result = %#v", args, result)
	}
}

func assertOperatorFailure(t *testing.T, args []string, operation, exitClass string, code int) {
	t.Helper()
	result, gotCode := operatorResult(t, args)
	if gotCode != code {
		t.Fatalf("%v exit code = %d, want %d; result = %#v", args, gotCode, code, result)
	}
	if result["operation"] != operation || result["success"] != false || result["exit_class"] != exitClass {
		t.Fatalf("%v result = %#v", args, result)
	}
}

func operatorResult(t *testing.T, args []string) (map[string]any, int) {
	t.Helper()
	var stdout bytes.Buffer
	code := run(args, &stdout, &bytes.Buffer{})
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("%v result is not JSON: %v; output=%q", args, err, stdout.String())
	}
	if result["operation_id"] == "" {
		t.Fatalf("%v lacks operation identity: %#v", args, result)
	}
	return result, code
}

func writeBackupFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
