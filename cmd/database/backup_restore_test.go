package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRestoreWorkflowCreatesAndRestoresCompleteArtifactWithTerminalConfirmation(t *testing.T) {
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

func TestBackupCreateRejectsMissingSource(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "instance.tar")
	missingSource := filepath.Join(t.TempDir(), "missing")

	assertOperatorFailure(t, []string{"backup", "create", "--data-dir", missingSource, "--output", archive}, "backup create", "operation_failed", 1)
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("failed backup created artifact: %v", err)
	}
}

func TestBackupCreateRejectsInvalidOutput(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "preserved")
	output := t.TempDir()

	assertOperatorFailure(t, []string{"backup", "create", "--data-dir", source, "--output", output}, "backup create", "operation_failed", 1)
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatalf("read invalid output directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed backup wrote output entries: %#v", entries)
	}
}

func TestRestoreRejectsUnsafeArtifact(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "unsafe.tar")
	writeUnsafeArchive(t, archive)

	assertOperatorFailure(t, []string{"restore", "--input", archive, "--data-dir", filepath.Join(t.TempDir(), "restored")}, "restore", "operation_failed", 1)
}

func TestRestoreRejectsMalformedArtifact(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "malformed.tar")
	writeBackupFixture(t, archive, "not a tar archive")

	assertOperatorFailure(t, []string{"restore", "--input", archive, "--data-dir", filepath.Join(t.TempDir(), "restored")}, "restore", "operation_failed", 1)
}

func TestRestoreReportsExtractionFailure(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "conflicting.tar")
	writeExtractionFailureArchive(t, archive)
	destination := filepath.Join(t.TempDir(), "restored")

	assertOperatorFailure(t, []string{"restore", "--input", archive, "--data-dir", destination}, "restore", "operation_failed", 1)
	if _, err := os.Stat(filepath.Join(destination, "entry")); err != nil {
		t.Fatalf("restore did not preserve first archive entry before extraction failure: %v", err)
	}
}

func TestBackupWorkflowReportsDistinctTerminalOperationIdentities(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "preserved")
	archive := filepath.Join(t.TempDir(), "instance.tar")

	createResult := assertOperatorSuccess(t, []string{"backup", "create", "--data-dir", source, "--output", archive}, "backup create")
	inspectResult := assertOperatorSuccess(t, []string{"backup", "inspect", "--input", archive}, "backup inspect")
	if createResult["operation_id"] == inspectResult["operation_id"] {
		t.Fatalf("workflow confirmations reused operation identity %q", createResult["operation_id"])
	}
}

func assertOperatorSuccess(t *testing.T, args []string, operation string) map[string]any {
	t.Helper()
	result, code := operatorResultForTest(t, args)
	if code != 0 {
		t.Fatalf("%v exit code = %d, result = %#v", args, code, result)
	}
	if result["operation"] != operation || result["success"] != true || result["exit_class"] != "success" {
		t.Fatalf("%v result = %#v", args, result)
	}
	return result
}

func assertOperatorFailure(t *testing.T, args []string, operation, exitClass string, code int) {
	t.Helper()
	result, gotCode := operatorResultForTest(t, args)
	if gotCode != code {
		t.Fatalf("%v exit code = %d, want %d; result = %#v", args, gotCode, code, result)
	}
	if result["operation"] != operation || result["success"] != false || result["exit_class"] != exitClass {
		t.Fatalf("%v result = %#v", args, result)
	}
}

func operatorResultForTest(t *testing.T, args []string) (map[string]any, int) {
	t.Helper()
	var stdout bytes.Buffer
	code := run(args, &stdout, &bytes.Buffer{})
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("%v result is not JSON: %v; output=%q", args, err, stdout.String())
	}
	if result["schema"] != "database.operator.result/v1" || result["operation_id"] == "" {
		t.Fatalf("%v lacks operation identity: %#v", args, result)
	}
	return result, code
}

func writeUnsafeArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	archive := tar.NewWriter(file)
	if err := archive.WriteHeader(&tar.Header{Name: "../outside", Mode: 0o600, Size: int64(len("unsafe"))}); err != nil {
		t.Fatalf("write archive header: %v", err)
	}
	if _, err := archive.Write([]byte("unsafe")); err != nil {
		t.Fatalf("write archive contents: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}
}

func writeExtractionFailureArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	archive := tar.NewWriter(file)
	writeArchiveEntry(t, archive, "entry", "file")
	writeArchiveEntry(t, archive, "entry/nested", "conflict")
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}
}

func writeArchiveEntry(t *testing.T, archive *tar.Writer, name, contents string) {
	t.Helper()
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(contents))}); err != nil {
		t.Fatalf("write archive header: %v", err)
	}
	if _, err := archive.Write([]byte(contents)); err != nil {
		t.Fatalf("write archive contents: %v", err)
	}
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
