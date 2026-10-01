package catalog

import (
	"reflect"
	"testing"
)

// Point lookups resolve the table by canonical identity, so any equivalent
// spelling of the namespace and table reaches the declared table's rows.
func TestStoreLookupsUseCanonicalTableIdentity(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Rows().Close()
	if err := store.CreateNamespace("App"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTableWithTypes("App", "Café", []string{"id", "name"}, []string{"INT", "VARCHAR(20)"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyDurable(func(definition Definition) (Definition, error) {
		namespace := definition.Namespaces[Key("App")]
		table := namespace.Tables[Key("Café")]
		table.Constraints = []Constraint{
			{Name: "PRIMARY", Type: ConstraintTypePrimary, Columns: []string{"id"}},
			{Name: "name", Type: ConstraintTypeUnique, Columns: []string{"name"}},
		}
		table.Rows = [][]string{{"1", "alpha"}}
		namespace.Tables[Key("Café")] = table
		return definition, nil
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{"1", "alpha"}
	for _, spelling := range [][2]string{{"App", "Café"}, {"app", "CAFÉ"}, {"APP", "café"}} {
		ref := NewTableRef(spelling[0], spelling[1])
		if row, ok := store.LookupPrimary(ref, "1"); !ok || !reflect.DeepEqual(row, want) {
			t.Fatalf("LookupPrimary through %q.%q = %v, %v", spelling[0], spelling[1], row, ok)
		}
		if row, ok := store.LookupUnique(ref, "name", "alpha"); !ok || !reflect.DeepEqual(row, want) {
			t.Fatalf("LookupUnique through %q.%q = %v, %v", spelling[0], spelling[1], row, ok)
		}
	}
	if _, ok := store.LookupPrimary(NewTableRef("App", "missing"), "1"); ok {
		t.Fatal("LookupPrimary found a row in a missing table")
	}
}
