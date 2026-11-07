package storage_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/internal/storage"
)

func TestEngineRecoveryAfterReplacementClear(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if err := engine.EnsureTable("app", "items", []string{"id"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	initial, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := initial.Insert("app", "items", []string{id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := initial.Commit(); err != nil {
		t.Fatal(err)
	}
	walPath := filepath.Join(directory, "rows", "wal.log")
	before, err := os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Clear("app", "items"); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Insert("app", "items", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	start := int(before.Size())
	clearEnd := walRecordEnd(t, wal, start, 3)
	if err := os.Truncate(walPath, int64(clearEnd)); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatalf("reopen after the complete clear record: %v", err)
	}
	defer reopened.Close()
	_, hasA := reopened.LookupPrimary("app", "items", "a")
	_, hasB := reopened.LookupPrimary("app", "items", "b")
	count := reopened.RowCount("app", "items")
	if !hasA || !hasB || count != 2 {
		t.Fatalf("after the complete clear record: a=%t b=%t count=%d; want old rows a,b", hasA, hasB, count)
	}
}

func walRecordEnd(t *testing.T, wal []byte, start int, kind byte) int {
	t.Helper()
	if len(wal)-start < 10 {
		t.Fatalf("incomplete record at byte %d", start)
	}
	end := start + 8 + int(binary.LittleEndian.Uint32(wal[start:start+4]))
	if end > len(wal) || wal[start+8] != 0xd2 || wal[start+9] != kind {
		t.Fatalf("expected complete versioned record kind %d at byte %d", kind, start)
	}
	return end
}
