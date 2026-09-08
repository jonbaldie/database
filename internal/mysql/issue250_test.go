package mysql

import (
	"reflect"
	"strings"
	"testing"
)

func TestIssue250NegativeIntegerLiteralsAreConstantExpressions(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue250", []string{"id", "a", "b"}, []string{"INT", "INT", "INT"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"1", "10", "3"}, {"2", "20", "1"}, {"3", "10", "3"}} {
		if err := store.Insert("app", "issue250", row); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeStatement(executor, "SELECT id FROM issue250 ORDER BY -1")
	if err != nil {
		t.Fatalf("ORDER BY -1: %v", err)
	}
	if want := [][]string{{"1"}, {"2"}, {"3"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY -1 rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT COUNT(*) FROM issue250 GROUP BY -1")
	if err != nil {
		t.Fatalf("GROUP BY -1: %v", err)
	}
	if want := [][]string{{"3"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("GROUP BY -1 rows = %#v, want %#v", result.rows, want)
	}
}

func TestIssue250InvalidOrdinalsStillRejected(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue250_ordinal", []string{"id", "a"}, []string{"INT", "INT"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "issue250_ordinal", []string{"1", "10"}); err != nil {
		t.Fatal(err)
	}

	_, err := executeStatement(executor, "SELECT id, a FROM issue250_ordinal ORDER BY 3")
	if err == nil || !strings.Contains(err.Error(), "Unknown column '3' in 'order clause'") {
		t.Fatalf("ORDER BY 3 error = %v, want unknown column in 'order clause'", err)
	}
	_, err = executeStatement(executor, "SELECT id, a FROM issue250_ordinal ORDER BY 0")
	if err == nil || !strings.Contains(err.Error(), "Unknown column '0' in 'order clause'") {
		t.Fatalf("ORDER BY 0 error = %v, want unknown column in 'order clause'", err)
	}
	_, err = executeStatement(executor, "SELECT id, a FROM issue250_ordinal GROUP BY 3")
	if err == nil || !strings.Contains(err.Error(), "Unknown column '3' in 'group statement'") {
		t.Fatalf("GROUP BY 3 error = %v, want unknown column in 'group statement'", err)
	}
}