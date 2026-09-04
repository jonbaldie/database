package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/instance"
)

func TestBackupRestoreStreamsBeyondMemoryLimit(t *testing.T) {
	if os.Getenv("DATABASE_STREAM_BACKUP_HELPER") == "1" {
		runStreamingBackupHelper(t, os.Getenv("DATABASE_STREAM_BACKUP_ROOT"))
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	command := exec.Command(executable, "-test.run=^TestBackupRestoreStreamsBeyondMemoryLimit$", "-test.v")
	command.Env = append(os.Environ(),
		"DATABASE_STREAM_BACKUP_HELPER=1",
		"DATABASE_STREAM_BACKUP_ROOT="+root,
		"GOMEMLIMIT=32MiB",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("capped-memory helper failed: %v\n%s", err, output)
	}
}

func runStreamingBackupHelper(t *testing.T, root string) {
	t.Helper()
	const payloadSize = int64(96 * 1024 * 1024)
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	writeInstanceFixture(t, source)
	payload := filepath.Join(source, "payload.bin")
	if err := os.WriteFile(payload, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(payload, payloadSize); err != nil {
		t.Fatal(err)
	}
	var peak atomic.Uint64
	done := make(chan struct{})
	go sampleHeapPeak(done, &peak)
	archive := filepath.Join(root, "instance.tar")
	if err := createBackup(source, archive); err != nil {
		close(done)
		t.Fatal(err)
	}
	destination := filepath.Join(root, "restored")
	if _, err := restoreBackup(archive, destination); err != nil {
		close(done)
		t.Fatal(err)
	}
	close(done)
	if info, err := os.Stat(filepath.Join(destination, "payload.bin")); err != nil || info.Size() != payloadSize {
		t.Fatalf("restored payload: info=%v err=%v", info, err)
	}
	if used := peak.Load(); used > 64*1024*1024 {
		t.Fatalf("peak heap = %d bytes, want at most 64 MiB", used)
	}
}

func sampleHeapPeak(done <-chan struct{}, peak *atomic.Uint64) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		for current := peak.Load(); stats.HeapAlloc > current && !peak.CompareAndSwap(current, stats.HeapAlloc); current = peak.Load() {
		}
		select {
		case <-done:
			return
		case <-ticker.C:
		}
	}
}

func TestBackupRestoreWorkflowCreatesAndRestoresCompleteArtifactWithTerminalConfirmation(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "nested", "record.txt"), "preserved")
	writeInstanceFixture(t, source)
	writeBackupFixture(t, filepath.Join(source, ".database-state"), "running\n")
	archive := filepath.Join(t.TempDir(), "instance.tar")
	if err := createBackup(source, archive); err != nil {
		t.Fatalf("create offline fixture backup: %v", err)
	}
	assertOperatorSuccess(t, []string{"backup", "inspect", "--backup", archive}, "backup inspect")

	destination := filepath.Join(t.TempDir(), "restored")
	assertOperatorSuccess(t, []string{"restore", "--backup", archive, "--data-directory", destination}, "restore")
	contents, err := os.ReadFile(filepath.Join(destination, "nested", "record.txt"))
	if err != nil {
		t.Fatalf("read restored artifact: %v", err)
	}
	if got := string(contents); got != "preserved" {
		t.Fatalf("restored contents = %q, want preserved", got)
	}
	if _, err := os.Stat(filepath.Join(destination, ".database-state")); !os.IsNotExist(err) {
		t.Fatalf("restored runtime state marker: %v", err)
	}
	sourceMetadata, err := instance.Load(source)
	if err != nil {
		t.Fatalf("load source metadata: %v", err)
	}
	restoredMetadata, err := instance.Load(destination)
	if err != nil {
		t.Fatalf("load restored metadata: %v", err)
	}
	if restoredMetadata.InstanceID == sourceMetadata.InstanceID || restoredMetadata.SourceInstanceID != sourceMetadata.InstanceID || restoredMetadata.State != "stopped" {
		t.Fatalf("restored identity = %#v, source = %#v", restoredMetadata, sourceMetadata)
	}
}

func TestRestoreRejectsNonEmptyDestination(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "preserved")
	writeInstanceFixture(t, source)
	archive := filepath.Join(t.TempDir(), "instance.tar")
	if err := createBackup(source, archive); err != nil {
		t.Fatalf("create offline fixture backup: %v", err)
	}

	destination := t.TempDir()
	writeBackupFixture(t, filepath.Join(destination, "existing.txt"), "existing")
	assertOperatorFailure(t, []string{"restore", "--backup", archive, "--data-directory", destination}, "restore", "precondition", 3)
}

func TestBackupInspectRejectsInvalidArtifact(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "invalid.tar")
	writeBackupFixture(t, archive, "not a tar archive")

	assertOperatorFailure(t, []string{"backup", "inspect", "--backup", archive}, "backup inspect", "operation_failed", 1)
}

func TestBackupCreateAcceptsOnlineConnectionInputs(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "instance.tar")
	passwordFile := filepath.Join(t.TempDir(), "password")
	writeBackupFixture(t, passwordFile, "online-backup-secret")

	result, code := operatorResultForTest(t, []string{
		"backup", "create",
		"--address=127.0.0.1:1",
		"--account=admin",
		"--password-file", passwordFile,
		"--output", archive,
		"--result=json",
	})
	if result["exit_class"] == "invalid_input" {
		t.Fatalf("online backup inputs rejected as invalid_input: %#v", result)
	}
	if code != 4 || result["exit_class"] != "access" {
		t.Fatalf("unreachable address result = %#v code=%d, want access/4", result, code)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("failed online backup created artifact: %v", err)
	}
}

func TestBackupCreateRejectsOfflineDataDirectoryInput(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "instance.tar")
	source := t.TempDir()
	writeInstanceFixture(t, source)

	assertOperatorFailure(t, []string{
		"backup", "create",
		"--data-dir", source,
		"--output", archive,
		"--result=json",
	}, "backup create", "invalid_input", 2)
}

func TestBackupCreateRejectsExistingOutput(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "instance.tar")
	writeBackupFixture(t, archive, "existing")
	passwordFile := filepath.Join(t.TempDir(), "password")
	writeBackupFixture(t, passwordFile, "online-backup-secret")
	assertOperatorFailure(t, []string{
		"backup", "create",
		"--address=127.0.0.1:1",
		"--account=admin",
		"--password-file", passwordFile,
		"--output", archive,
		"--result=json",
	}, "backup create", "precondition", 3)
}

func TestBackupCreateRejectsMissingAccount(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "instance.tar")
	passwordFile := filepath.Join(t.TempDir(), "password")
	writeBackupFixture(t, passwordFile, "online-backup-secret")
	assertOperatorFailure(t, []string{
		"backup", "create",
		"--address=127.0.0.1:3306",
		"--password-file", passwordFile,
		"--output", archive,
		"--result=json",
	}, "backup create", "invalid_input", 2)
}

func TestRestoreRejectsUnsafeArtifact(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "unsafe.tar")
	writeUnsafeArchive(t, archive)

	assertOperatorFailure(t, []string{"restore", "--backup", archive, "--data-directory", filepath.Join(t.TempDir(), "restored")}, "restore", "operation_failed", 1)
}

func TestRestoreRejectsMalformedArtifact(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "malformed.tar")
	writeBackupFixture(t, archive, "not a tar archive")

	assertOperatorFailure(t, []string{"restore", "--backup", archive, "--data-directory", filepath.Join(t.TempDir(), "restored")}, "restore", "operation_failed", 1)
}

func TestRestoreReportsExtractionFailure(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "conflicting.tar")
	writeExtractionFailureArchive(t, archive)
	destination := filepath.Join(t.TempDir(), "restored")

	assertOperatorFailure(t, []string{"restore", "--backup", archive, "--data-directory", destination}, "restore", "operation_failed", 1)
	if _, err := os.Stat(filepath.Join(destination, "entry")); !os.IsNotExist(err) {
		t.Fatalf("invalid restore left a usable entry: %v", err)
	}
}

func TestBackupWorkflowReportsDistinctTerminalOperationIdentities(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "preserved")
	writeInstanceFixture(t, source)
	archive := filepath.Join(t.TempDir(), "instance.tar")
	if err := createBackup(source, archive); err != nil {
		t.Fatalf("create offline fixture backup: %v", err)
	}

	inspectFirst := assertOperatorSuccess(t, []string{"backup", "inspect", "--backup", archive}, "backup inspect")
	inspectSecond := assertOperatorSuccess(t, []string{"backup", "inspect", "--backup", archive}, "backup inspect")
	if inspectFirst["operation_id"] == inspectSecond["operation_id"] {
		t.Fatalf("workflow confirmations reused operation identity %q", inspectFirst["operation_id"])
	}
}

func TestUpgradeWorkflowRequiresMatchingBackupAndCompletesForwardOnly(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "preserved")
	writeInstanceFixture(t, source)
	archive := filepath.Join(t.TempDir(), "instance.tar")
	if err := createBackup(source, archive); err != nil {
		t.Fatalf("create offline fixture backup: %v", err)
	}

	assertOperatorSuccess(t, []string{"upgrade", "--data-directory", source, "--backup", archive, "--target-version", "0.1.1", "--yes"}, "upgrade")
	metadata, err := instance.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.DataVersion != "0.1.1" || metadata.State != "stopped" {
		t.Fatalf("upgraded metadata = %#v", metadata)
	}
	if _, err := os.Stat(filepath.Join(source, instance.UpgradeIncompleteMarker)); !os.IsNotExist(err) {
		t.Fatalf("upgrade marker remains: %v", err)
	}
}

func TestUpgradeRejectsChangedSourceAndUnsupportedWorkflows(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "before")
	writeInstanceFixture(t, source)
	archive := filepath.Join(t.TempDir(), "instance.tar")
	if err := createBackup(source, archive); err != nil {
		t.Fatalf("create offline fixture backup: %v", err)
	}
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "after")

	assertOperatorFailure(t, []string{"upgrade", "--data-directory", source, "--backup", archive, "--target-version", "0.1.1", "--yes"}, "upgrade", "precondition", 3)
	assertOperatorFailure(t, []string{"upgrade", "--data-directory", source, "--backup", archive, "--target-version", "0.1.1", "--yes", "--rolling"}, "upgrade", "precondition", 3)
	assertOperatorFailure(t, []string{"upgrade", "--data-directory", source, "--backup", archive, "--target-version", "0.1.1"}, "upgrade", "invalid_input", 2)
}

func TestUpgradeResumesOnlyTheMarkedTarget(t *testing.T) {
	source := t.TempDir()
	writeBackupFixture(t, filepath.Join(source, "record.txt"), "preserved")
	writeInstanceFixture(t, source)
	archive := filepath.Join(t.TempDir(), "instance.tar")
	if err := createBackup(source, archive); err != nil {
		t.Fatalf("create offline fixture backup: %v", err)
	}
	metadata, err := instance.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	metadata.State = "upgrade-incomplete"
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "instance.json"), metadataBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	marker := upgradeMarker{Schema: upgradeMarkerSchema, TargetVersion: "0.1.1", SourceInstanceID: metadata.InstanceID, StartedAt: "2026-07-31T00:00:00Z"}
	markerBytes, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, instance.UpgradeIncompleteMarker), markerBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	assertOperatorFailure(t, []string{"upgrade", "--data-directory", source, "--backup", archive, "--target-version", "0.1.2", "--yes"}, "upgrade", "precondition", 3)
	assertOperatorSuccess(t, []string{"upgrade", "--data-directory", source, "--backup", archive, "--target-version", "0.1.1", "--yes"}, "upgrade")
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

func writeInstanceFixture(t *testing.T, directory string) {
	t.Helper()
	metadata := `{"schema":"database.instance/v1","instance_id":"source-instance","state":"stopped","admin_account":"admin","password_hash":"password-hash"}`
	if err := os.WriteFile(filepath.Join(directory, "instance.json"), []byte(metadata), 0o600); err != nil {
		t.Fatalf("write instance metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "catalog.json"), []byte(`{"namespaces":{},"accounts":{}}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
}
