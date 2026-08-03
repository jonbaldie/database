package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverRemovesAbandonedCatalogSnapshot(t *testing.T) {
	directory := t.TempDir()
	temporary := filepath.Join(directory, ".catalog-crash.tmp")
	if err := os.WriteFile(temporary, []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Recover(directory); err != nil {
		t.Fatalf("recover catalog: %v", err)
	}
	if _, err := Open(directory); err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned catalog snapshot remains: %v", err)
	}
}

func TestReplaceRowsClearAllowsReinsert(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTable("app", "items", []string{"id", "name"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "items", []string{"1", "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceRows("app", "items", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "items", []string{"1", "alpha"}); err != nil {
		t.Fatalf("reinsert after clear: %v", err)
	}
	rows := store.Snapshot().Namespaces["app"].Tables["items"].Rows
	if len(rows) != 1 || rows[0][0] != "1" {
		t.Fatalf("rows after reinsert = %#v", rows)
	}
}
