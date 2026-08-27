package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestApplyCopiesOnlyChangedPrimaryIndex(t *testing.T) {
	first := Table{
		Name:        "first",
		Columns:     []string{"id"},
		Constraints: []Constraint{{Name: "PRIMARY", Type: ConstraintTypePrimary, Columns: []string{"id"}}},
		Rows:        [][]string{{"1"}},
	}
	second := Table{
		Name:        "second",
		Columns:     []string{"id"},
		Constraints: []Constraint{{Name: "PRIMARY", Type: ConstraintTypePrimary, Columns: []string{"id"}}},
		Rows:        [][]string{{"2"}},
	}
	RebuildPrimaryIndex(&first)
	RebuildPrimaryIndex(&second)
	source := Definition{Namespaces: map[string]Namespace{
		"app": {Name: "app", Tables: map[string]Table{"first": first, "second": second}},
	}}

	staged, err := Apply(source, func(definition *Definition) error {
		namespace := definition.Namespaces["app"]
		table := namespace.Tables["first"]
		previousLength := len(table.Rows)
		previousIndex := table.PrimaryIndex
		table.Rows = append(table.Rows, []string{"3"})
		MaintainPrimaryIndex(&table, previousLength, previousIndex)
		namespace.Tables["first"] = table
		definition.Namespaces["app"] = namespace
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	changed := staged.Namespaces["app"].Tables["first"].PrimaryIndex
	unchanged := staged.Namespaces["app"].Tables["second"].PrimaryIndex
	if reflect.ValueOf(changed).Pointer() == reflect.ValueOf(first.PrimaryIndex).Pointer() {
		t.Fatal("changed table still shares its primary index")
	}
	if reflect.ValueOf(unchanged).Pointer() != reflect.ValueOf(second.PrimaryIndex).Pointer() {
		t.Fatal("unchanged table copied its primary index")
	}
}

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
