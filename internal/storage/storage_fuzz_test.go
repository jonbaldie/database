package storage

import (
	"fmt"
	"sort"
	"testing"
)

func FuzzStorageTransactionsMatchModel(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7})
	f.Add([]byte{0, 0, 1, 4, 2, 1, 3, 0, 4})
	f.Add([]byte{1, 1, 1, 1, 2, 2, 2, 3, 4})
	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 64 {
			operations = operations[:64]
		}
		directory := t.TempDir()
		engine, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.EnsureTable("app", "items", []string{"id", "name"}, []string{"id"}, [][]string{{"name"}}); err != nil {
			t.Fatal(err)
		}
		model := map[string][]string{}
		for step, operation := range operations {
			switch operation % 5 {
			case 0:
				id := fmt.Sprintf("insert-%d", step)
				row := []string{id, fmt.Sprintf("value-%d-%d", step, operation)}
				txn, err := engine.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if err := txn.Insert("app", "items", row); err != nil {
					t.Fatal(err)
				}
				if err := txn.Commit(); err != nil {
					t.Fatal(err)
				}
				model[id] = row
			case 1:
				updateStorageModel(t, engine, model, step, operation)
			case 2:
				deleteStorageModel(t, engine, model, operation)
			case 3:
				txn, err := engine.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if err := txn.Clear("app", "items"); err != nil {
					t.Fatal(err)
				}
				if err := txn.Commit(); err != nil {
					t.Fatal(err)
				}
				model = map[string][]string{}
			case 4:
				engine = reopenStorageModel(t, engine, directory)
			}
			assertStorageModel(t, engine, model)
		}
		engine = reopenStorageModel(t, engine, directory)
		assertStorageModel(t, engine, model)
		if err := engine.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func updateStorageModel(t *testing.T, engine *Engine, model map[string][]string, step int, operation byte) {
	t.Helper()
	keys := storageModelKeys(model)
	if len(keys) == 0 {
		return
	}
	oldID := keys[int(operation)%len(keys)]
	otherID := keys[(int(operation)+1)%len(keys)]
	newID := oldID
	if operation&1 != 0 {
		newID = fmt.Sprintf("update-%d", step)
	}
	if operation&2 != 0 && len(keys) > 1 {
		newID = otherID
	}
	newName := fmt.Sprintf("updated-%d-%d", step, operation)
	if operation&4 != 0 && len(keys) > 1 {
		newName = model[otherID][1]
	}
	next := []string{newID, newName}
	conflict := newID != oldID && model[newID] != nil
	if newName != model[oldID][1] {
		for id, row := range model {
			if id != oldID && row[1] == newName {
				conflict = true
			}
		}
	}
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.UpdatePrimary("app", "items", oldID, next); err != nil {
		t.Fatal(err)
	}
	err = txn.Commit()
	if conflict {
		if err == nil {
			t.Fatal("conflicting update succeeded")
		}
		return
	}
	if err != nil {
		t.Fatalf("update commit: %v", err)
	}
	delete(model, oldID)
	model[newID] = next
}

func deleteStorageModel(t *testing.T, engine *Engine, model map[string][]string, operation byte) {
	t.Helper()
	keys := storageModelKeys(model)
	if len(keys) == 0 {
		return
	}
	id := keys[int(operation)%len(keys)]
	txn, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.DeletePrimary("app", "items", id); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	delete(model, id)
}

func reopenStorageModel(t *testing.T, engine *Engine, directory string) *Engine {
	t.Helper()
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	return reopened
}

func assertStorageModel(t *testing.T, engine *Engine, model map[string][]string) {
	t.Helper()
	rows, ok := engine.SnapshotRows("app", "items")
	if !ok {
		t.Fatal("storage table is missing")
	}
	if len(rows) != len(model) {
		t.Fatalf("row count = %d, want %d", len(rows), len(model))
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if len(row) != 2 || seen[row[0]] {
			t.Fatalf("invalid stored rows: %q", rows)
		}
		seen[row[0]] = true
		want, exists := model[row[0]]
		if !exists || want[1] != row[1] {
			t.Fatalf("stored row = %q, want %q", row, want)
		}
	}
	for id := range model {
		if !seen[id] {
			t.Fatalf("model row %q is missing from storage", id)
		}
	}
}

func storageModelKeys(model map[string][]string) []string {
	keys := make([]string, 0, len(model))
	for key := range model {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
