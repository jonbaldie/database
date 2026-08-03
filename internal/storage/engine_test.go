package storage_test

import (
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/internal/storage"
)

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
