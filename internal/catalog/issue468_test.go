package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsCorruptCatalogJson(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "catalog.json")

	// Invalid JSON
	if err := os.WriteFile(path, []byte("invalid-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open invalid JSON error = %v, want ErrCorrupt", err)
	}

	// Null namespaces
	if err := os.WriteFile(path, []byte(`{"namespaces":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open missing namespaces error = %v, want ErrCorrupt", err)
	}

	// Invalid definition (namespace with nil tables)
	if err := os.WriteFile(path, []byte(`{"namespaces":{"main":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open invalid definition error = %v, want ErrCorrupt", err)
	}
}

func TestOpenRejectsCorruptStorage(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "catalog.json"), []byte(`{"namespaces":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "rows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "rows", "tables.meta"), []byte("invalid-meta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open with corrupted tables.meta error = %v, want ErrCorrupt", err)
	}
}
