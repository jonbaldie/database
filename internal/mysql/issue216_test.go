package mysql

import (
	"reflect"
	"testing"
)

func TestIssue216MixedOuterJoinChains(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	for _, table := range []struct {
		name    string
		columns []string
		types   []string
	}{
		{name: "issue216_a", columns: []string{"id"}, types: []string{"INT"}},
		{name: "issue216_b", columns: []string{"id", "a_ref"}, types: []string{"INT", "INT"}},
		{name: "issue216_c", columns: []string{"b_ref", "label"}, types: []string{"INT", "VARCHAR(32)"}},
	} {
		if err := store.CreateTableWithTypes("app", table.name, table.columns, table.types); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]string{{"1"}, {"2"}, {"3"}} {
		if err := store.Insert("app", "issue216_a", row); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]string{{"10", "1"}, {"20", "2"}} {
		if err := store.Insert("app", "issue216_b", row); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Insert("app", "issue216_c", []string{"10", "first"}); err != nil {
		t.Fatal(err)
	}

	query := "SELECT a.id, b.id, c.label FROM issue216_a a JOIN issue216_b b ON b.a_ref = a.id LEFT JOIN issue216_c c ON c.b_ref = b.id ORDER BY a.id, b.id"
	result, err := executeStatement(executor, query)
	if err != nil {
		t.Fatalf("mixed inner/left join: %v", err)
	}
	if want := [][]string{{"1", "10", "first"}, {"2", "20", "NULL"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("mixed inner/left rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT a.id, b.id, c.label FROM issue216_a a LEFT JOIN issue216_b b ON b.a_ref = a.id LEFT JOIN issue216_c c ON c.b_ref = b.id ORDER BY a.id, b.id")
	if err != nil {
		t.Fatalf("left/left join: %v", err)
	}
	if want := [][]string{{"1", "10", "first"}, {"2", "20", "NULL"}, {"3", "NULL", "NULL"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("left/left rows = %#v, want %#v", result.rows, want)
	}
}
