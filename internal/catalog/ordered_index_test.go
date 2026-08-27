package catalog

import "testing"

func TestOrderedIndexCacheIsSharedUntilRowsChange(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.rows.Close() })
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTableWithTypes("app", "entries", []string{"id"}, []string{"INT"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "entries", []string{"2"}); err != nil {
		t.Fatal(err)
	}

	first := store.Snapshot().Namespaces["app"].Tables["entries"]
	builds := 0
	build := func() ([]OrderedIndexRow, error) {
		builds++
		return []OrderedIndexRow{{Position: 0, Keys: []string{"2"}}}, nil
	}
	if _, err := first.CachedOrderedIndex("idx_id", build); err != nil {
		t.Fatal(err)
	}
	second := store.Snapshot().Namespaces["app"].Tables["entries"]
	if _, err := second.CachedOrderedIndex("idx_id", build); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatalf("unchanged row image built index %d times, want 1", builds)
	}

	if err := store.Insert("app", "entries", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	changed := store.Snapshot().Namespaces["app"].Tables["entries"]
	if _, err := changed.CachedOrderedIndex("idx_id", build); err != nil {
		t.Fatal(err)
	}
	if builds != 2 {
		t.Fatalf("changed row image built index %d times, want 2", builds)
	}
	if _, err := first.CachedOrderedIndex("idx_id", build); err != nil {
		t.Fatal(err)
	}
	if builds != 2 {
		t.Fatalf("immutable snapshot rebuilt index: builds = %d", builds)
	}
}
