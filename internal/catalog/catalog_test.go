package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRemovesAbandonedCatalogSnapshot(t *testing.T) {
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
