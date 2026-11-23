package storage

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"strings"
	"testing"
)

func TestWALRecoveryRejectsMalformedMarkers(t *testing.T) {
	for _, kind := range []byte{walBegin, walCommit} {
		cases := map[string][]byte{
			"namespace": encodePayload(kind, "app", "", nil),
			"table":     encodePayload(kind, "", "items", nil),
			"row":       encodePayload(kind, "", "", []string{""}),
			"legacy":    {kind, 0, 0},
			"trailing":  append(encodePayload(kind, "", "", nil), 0),
		}
		for name, payload := range cases {
			t.Run(string(rune('0'+kind))+"/"+name, func(t *testing.T) {
				directory := newWALReplayDirectory(t)
				appendWALTestPayload(t, directory, encodePayload(walBegin, "", "", nil))
				appendWALTestPayload(t, directory, payload)
				requireWALOpenError(t, directory)
			})
		}
	}
}

func TestWALRecoveryRejectsInvalidHistory(t *testing.T) {
	cases := map[string][][]byte{
		"commit without begin": {encodePayload(walCommit, "", "", nil)},
		"second commit": {
			encodePayload(walBegin, "", "", nil),
			encodePayload(walCommit, "", "", nil),
			encodePayload(walCommit, "", "", nil),
		},
		"unknown pending operation": {
			encodePayload(walBegin, "", "", nil),
			encodePayload(7, "app", "items", nil),
		},
		"invalid row after clear": {
			encodePayload(walBegin, "", "", nil),
			encodePayload(walClear, "app", "items", nil),
			encodePayload(walInsert, "app", "items", []string{"a", "extra"}),
			encodePayload(walCommit, "", "", nil),
		},
		"missing table after clear": {
			encodePayload(walBegin, "", "", nil),
			encodePayload(walClear, "app", "items", nil),
			encodePayload(walInsert, "app", "missing", []string{"a"}),
			encodePayload(walCommit, "", "", nil),
		},
	}
	for name, payloads := range cases {
		t.Run(name, func(t *testing.T) {
			directory := newWALReplayDirectory(t)
			for _, payload := range payloads {
				appendWALTestPayload(t, directory, payload)
			}
			requireWALOpenError(t, directory)
		})
	}
}

func TestWALRecoveryEmptyTransaction(t *testing.T) {
	directory := newWALReplayDirectory(t)
	appendWALTestPayload(t, directory, encodePayload(walBegin, "", "", nil))
	appendWALTestPayload(t, directory, encodePayload(walCommit, "", "", nil))
	engine, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if got := engine.RowCount("app", "items"); got != 0 {
		t.Fatalf("empty transaction recovered %d rows", got)
	}
}

func TestWALRecoveryLegacyOperations(t *testing.T) {
	for _, versioned := range []bool{false, true} {
		t.Run(map[bool]string{false: "unversioned", true: "versioned"}[versioned], func(t *testing.T) {
			directory := newWALReplayDirectory(t)
			operations := []walOperation{
				{kind: walInsert, row: []string{"discard"}},
				{kind: walClear},
				{kind: walInsert, row: []string{"a"}},
				{kind: walInsert, row: []string{"b"}},
				{kind: walUpdate, row: []string{"a", "c"}},
				{kind: walDelete, row: []string{"b"}},
			}
			for _, operation := range operations {
				payload := encodePayload(operation.kind, "app", "items", operation.row)
				if !versioned {
					payload = append([]byte{operation.kind}, []byte("app\x00items\x00"+strings.Join(operation.row, "\x01"))...)
				}
				appendWALTestPayload(t, directory, payload)
			}
			engine, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			_, hasC := engine.LookupPrimary("app", "items", "c")
			if count := engine.RowCount("app", "items"); !hasC || count != 1 {
				t.Fatalf("legacy replay: c=%t count=%d; want only c", hasC, count)
			}
		})
	}
}

func newWALReplayDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	engine, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.EnsureTable("app", "items", []string{"id"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	return directory
}

func appendWALTestPayload(t *testing.T, directory string, payload []byte) {
	t.Helper()
	file, err := os.OpenFile(rowsWalPath(directory), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	frame := binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))
	frame = binary.LittleEndian.AppendUint32(frame, crc32.ChecksumIEEE(payload))
	if _, err := file.Write(append(frame, payload...)); err != nil {
		t.Fatal(err)
	}
}

func requireWALOpenError(t *testing.T, directory string) {
	t.Helper()
	engine, err := Open(directory)
	if engine != nil {
		_ = engine.Close()
		t.Fatal("Open returned a provisional engine for invalid WAL history")
	}
	if err == nil {
		t.Fatal("Open accepted invalid WAL history")
	}
}
