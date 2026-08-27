package storage_test

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jonbaldie/database/internal/storage"
)

func TestEnginePointUpdateDoesNotCopyEveryStoredRow(t *testing.T) {
	engine, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.EnsureTable("app", "items", []string{"id", "value"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for id := range 256 {
		value := strconv.Itoa(id)
		if err := txn.Insert("app", "items", []string{value, value}); err != nil {
			t.Fatal(err)
		}
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}

	var updateErr error
	allocations := testing.AllocsPerRun(3, func() {
		var update *storage.Transaction
		update, updateErr = engine.Begin()
		if updateErr != nil {
			return
		}
		if updateErr = update.UpdatePrimary("app", "items", "0", []string{"0", "changed"}); updateErr != nil {
			return
		}
		updateErr = update.Commit()
	})
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	if allocations >= 128 {
		t.Fatalf("point update allocations = %.0f, want fewer than 128", allocations)
	}
}

func TestEngineDoesNotRebuildUnchangedTableMetadata(t *testing.T) {
	engine, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	columns := []string{"id", "value"}
	primary := []string{"id"}
	uniques := [][]string{{"value"}}
	if err := engine.EnsureTable("app", "items", columns, primary, uniques); err != nil {
		t.Fatal(err)
	}

	var ensureErr error
	allocations := testing.AllocsPerRun(3, func() {
		ensureErr = engine.EnsureTable("app", "items", columns, primary, uniques)
	})
	if ensureErr != nil {
		t.Fatal(ensureErr)
	}
	if allocations >= 2 {
		t.Fatalf("unchanged EnsureTable allocations = %.0f, want fewer than 2", allocations)
	}
}

func TestEngineInsertLookupSurvivesReopen(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "name"}, []string{"id"}, [][]string{{"name"}}); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"2", "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	row, ok := engine.LookupPrimary("app", "items", "1")
	if !ok || row[1] != "alpha" {
		t.Fatalf("primary lookup = (%v, %v)", row, ok)
	}
	row, ok = engine.LookupUnique("app", "items", "name", "beta")
	if !ok || row[0] != "2" {
		t.Fatalf("unique lookup = (%v, %v)", row, ok)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, ok = reopened.LookupPrimary("app", "items", "2")
	if !ok || row[1] != "beta" {
		t.Fatalf("reopened primary lookup = (%v, %v)", row, ok)
	}
	if _, err := filepath.Rel(directory, directory); err != nil {
		t.Fatal(err)
	}
}

func TestEngineRejectsDuplicatePrimaryKey(t *testing.T) {
	engine, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.EnsureTable("app", "items", []string{"id"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1"}); err == nil {
		t.Fatal("expected duplicate primary key")
	}
}

func TestEngineEnforcesUniqueKeyWithoutPrimary(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "name"}, nil, [][]string{{"name"}}); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"2", "alpha"}); err == nil {
		t.Fatal("expected duplicate unique key without primary key")
	}
	if got := engine.RowCount("app", "items"); got != 1 {
		t.Fatalf("row count after rejected unique insert = %d", got)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.RowCount("app", "items"); got != 1 {
		t.Fatalf("reopened row count = %d", got)
	}
}

func TestEngineAllowsMultipleNullValuesInUniqueKey(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "code"}, []string{"id"}, [][]string{{"code"}}); err != nil {
		t.Fatal(err)
	}
	null := "\x00database-sql-null"
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"1", "alpha"}, {"2", null}} {
		if err := txn.Insert("app", "items", row); err != nil {
			t.Fatal(err)
		}
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.UpdatePrimary("app", "items", "1", []string{"1", null}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatalf("second NULL unique value: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.RowCount("app", "items"); got != 2 {
		t.Fatalf("reopened row count = %d", got)
	}
}

func TestEngineCompositeKeySeparatesControlBytes(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"first", "second"}, []string{"first", "second"}, nil); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"left\x00middle", "right"}, {"left", "middle\x00right"}} {
		if err := txn.Insert("app", "items", row); err != nil {
			t.Fatal(err)
		}
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := engine.RowCount("app", "items"); got != 2 {
		t.Fatalf("row count after composite inserts = %d", got)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.RowCount("app", "items"); got != 2 {
		t.Fatalf("reopened composite row count = %d", got)
	}
}

func TestEngineUpdateByPrimaryKeyIsDurable(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "name", "color"}, []string{"id"}, [][]string{{"name"}}); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1", "alpha", "red"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.UpdatePrimary("app", "items", "1", []string{"1", "alpha", "blue"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, ok := reopened.LookupPrimary("app", "items", "1")
	if !ok || row[2] != "blue" {
		t.Fatalf("updated row = (%v, %v)", row, ok)
	}
	row, ok = reopened.LookupUnique("app", "items", "name", "alpha")
	if !ok || row[2] != "blue" {
		t.Fatalf("unique after update = (%v, %v)", row, ok)
	}
}

func TestEnginePrimaryKeyUpdateIsDurable(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "name"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.UpdatePrimary("app", "items", "1", []string{"2", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.RowCount("app", "items"); got != 1 {
		t.Fatalf("reopened row count after primary update = %d", got)
	}
	if _, ok := reopened.LookupPrimary("app", "items", "1"); ok {
		t.Fatal("old primary key still exists after reopen")
	}
	row, ok := reopened.LookupPrimary("app", "items", "2")
	if !ok || len(row) != 2 || row[1] != "alpha" {
		t.Fatalf("reopened primary-key update = (%v, %v)", row, ok)
	}
}

func TestEngineClearAllowsReinsertAndSurvivesReopen(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "name"}, []string{"id"}, [][]string{{"name"}}); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Clear("app", "items"); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := engine.RowCount("app", "items"); got != 0 {
		t.Fatalf("row count after clear = %d", got)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, ok := reopened.LookupPrimary("app", "items", "1")
	if !ok || row[1] != "alpha" {
		t.Fatalf("reinserted row = (%v, %v)", row, ok)
	}
}

func TestEngineReopenPreservesControlBytesInValues(t *testing.T) {
	directory := t.TempDir()
	value := "left\x01right"
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "value"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.Insert("app", "items", []string{"1", value}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, ok := reopened.LookupPrimary("app", "items", "1")
	if !ok || len(row) != 2 || row[1] != value {
		t.Fatalf("reopened control-byte value = (%v, %v)", row, ok)
	}
}

func TestEngineRejectedStaleInsertDoesNotPoisonReopen(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id"}, []string{"id"}, nil); err != nil {
		t.Fatal(err)
	}
	first, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Insert("app", "items", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := second.Insert("app", "items", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(); err == nil {
		t.Fatal("expected stale duplicate insert to fail")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatalf("reopen after rejected insert: %v", err)
	}
	defer reopened.Close()
	if got := reopened.RowCount("app", "items"); got != 1 {
		t.Fatalf("reopened row count = %d", got)
	}
}

func TestEngineRejectedDuplicateUpdateDoesNotPoisonReopen(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "name"}, []string{"id"}, [][]string{{"name"}}); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"1", "alpha"}, {"2", "beta"}} {
		if err := txn.Insert("app", "items", row); err != nil {
			t.Fatal(err)
		}
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.UpdatePrimary("app", "items", "1", []string{"1", "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err == nil {
		t.Fatal("expected duplicate update to fail")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatalf("reopen after rejected update: %v", err)
	}
	defer reopened.Close()
	row, ok := reopened.LookupPrimary("app", "items", "1")
	if !ok || len(row) != 2 || row[1] != "alpha" {
		t.Fatalf("reopened row after rejected update = (%v, %v)", row, ok)
	}
}

func TestEngineRejectedUpdateDoesNotPartiallyMutateIndexes(t *testing.T) {
	directory := t.TempDir()
	engine, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.EnsureTable("app", "items", []string{"id", "name"}, []string{"id"}, [][]string{{"name"}}); err != nil {
		t.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"1", "alpha"}, {"2", "beta"}} {
		if err := txn.Insert("app", "items", row); err != nil {
			t.Fatal(err)
		}
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	txn, err = engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.UpdatePrimary("app", "items", "1", []string{"3", "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err == nil {
		t.Fatal("expected conflicting update to fail")
	}
	row, ok := engine.LookupPrimary("app", "items", "1")
	if !ok || len(row) != 2 || row[1] != "alpha" {
		t.Fatalf("row after rejected update = (%v, %v)", row, ok)
	}
	if _, ok := engine.LookupPrimary("app", "items", "3"); ok {
		t.Fatal("new primary key exists after rejected update")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	row, ok = reopened.LookupPrimary("app", "items", "1")
	if !ok || len(row) != 2 || row[1] != "alpha" {
		t.Fatalf("reopened row after rejected update = (%v, %v)", row, ok)
	}
}
