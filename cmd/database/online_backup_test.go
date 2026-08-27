package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonbaldie/database/internal/instance"
)

func TestOnlineBackupStreamsBeyondMemoryLimit(t *testing.T) {
	if os.Getenv("DATABASE_ONLINE_STREAM_BACKUP_HELPER") == "1" {
		runOnlineStreamingBackupHelper(t, os.Getenv("DATABASE_ONLINE_STREAM_BACKUP_ROOT"))
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	command := exec.Command(executable, "-test.run=^TestOnlineBackupStreamsBeyondMemoryLimit$", "-test.v")
	command.Env = append(os.Environ(),
		"DATABASE_ONLINE_STREAM_BACKUP_HELPER=1",
		"DATABASE_ONLINE_STREAM_BACKUP_ROOT="+root,
		"GOMEMLIMIT=32MiB",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("capped-memory online helper failed: %v\n%s", err, output)
	}
}

func runOnlineStreamingBackupHelper(t *testing.T, root string) {
	t.Helper()
	metadata := instance.Metadata{
		Schema: "database.instance/v1", InstanceID: "online-stream", State: "stopped",
		AdminAccount: "admin", PasswordHash: "hash", DataVersion: instance.CurrentDataVersion,
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "source-catalog.json")
	writeLargeOnlineCatalog(t, catalogPath)
	catalogFile, err := os.Open(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	rows := &onlineBackupTestRows{metadata: string(metadataBytes), catalog: catalogFile, buffer: make([]byte, 64*1024)}
	var peak atomic.Uint64
	done := make(chan struct{})
	go sampleHeapPeak(done, &peak)
	capture, err := captureOnlineBackupRows(rows)
	if err != nil {
		close(done)
		t.Fatal(err)
	}
	defer capture.Close()
	manifest := makeSourceBackupManifest(capture.metadata, capture.files)
	archive := filepath.Join(root, "online.tar")
	if err := writeSourceBackupArchive(archive, manifest, capture.files); err != nil {
		close(done)
		t.Fatal(err)
	}
	if err := inspectBackup(archive); err != nil {
		close(done)
		t.Fatal(err)
	}
	close(done)
	runtime.GC()
	if used := peak.Load(); used > 64*1024*1024 {
		t.Fatalf("peak heap = %d bytes, want at most 64 MiB", used)
	}
}

func writeLargeOnlineCatalog(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriterSize(file, 64*1024)
	if _, err := writer.WriteString(`{"namespaces":{"app":{"tables":{"items":{"columns":["value"],"rows":[`); err != nil {
		t.Fatal(err)
	}
	row := `["` + strings.Repeat("x", 1024) + `"]`
	for index := range 96 * 1024 {
		if index > 0 {
			if err := writer.WriteByte(','); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := writer.WriteString(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.WriteString("]}}}}}\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

type onlineBackupTestRows struct {
	metadata        string
	catalog         *os.File
	buffer          []byte
	path            string
	content         string
	metadataEmitted bool
	err             error
}

func (rows *onlineBackupTestRows) Next() bool {
	if !rows.metadataEmitted {
		rows.metadataEmitted = true
		rows.path, rows.content = "instance.json", rows.metadata
		return true
	}
	read, err := rows.catalog.Read(rows.buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		rows.err = err
		return false
	}
	if read == 0 {
		return false
	}
	rows.path, rows.content = "catalog.json", string(rows.buffer[:read])
	return true
}

func (rows *onlineBackupTestRows) Scan(destinations ...any) error {
	if len(destinations) != 2 {
		return fmt.Errorf("destination count = %d, want 2", len(destinations))
	}
	path, pathOK := destinations[0].(*string)
	content, contentOK := destinations[1].(*string)
	if !pathOK || !contentOK {
		return errors.New("backup destinations must be strings")
	}
	*path, *content = rows.path, rows.content
	return nil
}

func (rows *onlineBackupTestRows) Err() error { return rows.err }

func (rows *onlineBackupTestRows) Close() error { return rows.catalog.Close() }
