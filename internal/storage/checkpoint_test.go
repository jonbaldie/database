package storage

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCheckpointRecoveryAtEveryPublicationPhase(t *testing.T) {
	phases := []checkpointPhase{
		checkpointTempSynced,
		checkpointPublished,
		checkpointWALSynced,
		checkpointWALRotated,
	}
	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			directory := t.TempDir()
			engine := openCheckpointTestEngine(t, directory)
			for id := range checkpointEveryCommits - 1 {
				insertCheckpointTestRow(t, engine, id)
			}
			stopped := false
			engine.checkpointHook = func(current checkpointPhase) error {
				if current != phase {
					return nil
				}
				stopped = true
				return errString("simulated process stop")
			}
			insertCheckpointTestRow(t, engine, checkpointEveryCommits-1)
			if !stopped {
				t.Fatalf("checkpoint did not reach phase %q", phase)
			}
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if got := reopened.RowCount("app", "items"); got != checkpointEveryCommits {
				t.Fatalf("row count = %d, want %d", got, checkpointEveryCommits)
			}
			row, ok := reopened.LookupPrimary("app", "items", strconv.Itoa(checkpointEveryCommits-1))
			if !ok || row[0] != strconv.Itoa(checkpointEveryCommits-1) {
				t.Fatalf("latest row = (%v, %v)", row, ok)
			}
		})
	}
}

func TestCheckpointRecoveryReplaysOnlyWALTail(t *testing.T) {
	directory := t.TempDir()
	engine := openCheckpointTestEngine(t, directory)
	for id := range checkpointEveryCommits + 3 {
		insertCheckpointTestRow(t, engine, id)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.RowCount("app", "items"); got != checkpointEveryCommits+3 {
		t.Fatalf("row count = %d, want %d", got, checkpointEveryCommits+3)
	}
}

func TestCheckpointIgnoresIncompleteTemporaryPublication(t *testing.T) {
	directory := t.TempDir()
	engine := openCheckpointTestEngine(t, directory)
	insertCheckpointTestRow(t, engine, 0)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rowsGlobalCheckpointPath(directory)+".tmp", []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.RowCount("app", "items"); got != 1 {
		t.Fatalf("row count = %d, want 1", got)
	}
}

func TestCheckpointRejectsDamagedPublishedFile(t *testing.T) {
	directory := t.TempDir()
	engine := openCheckpointTestEngine(t, directory)
	for id := range checkpointEveryCommits {
		insertCheckpointTestRow(t, engine, id)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	path := rowsGlobalCheckpointPath(directory)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 0xff
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); err == nil {
		t.Fatal("expected damaged checkpoint to prevent recovery")
	}
	if _, err := os.Stat(filepath.Join(directory, "rows", "wal.log")); err != nil {
		t.Fatalf("WAL was not preserved: %v", err)
	}
}

func openCheckpointTestEngine(t *testing.T, directory string) *Engine {
	t.Helper()
	engine, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	return engine
}

func insertCheckpointTestRow(t *testing.T, engine *Engine, id int) {
	t.Helper()
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	value := strconv.Itoa(id)
	if err := txn.Insert("app", "items", []string{value}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
}
