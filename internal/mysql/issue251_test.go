package mysql

import (
	"reflect"
	"strings"
	"testing"
)

func TestIssue251OrderUsesAliasWithinExpression(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue251", []string{"id", "a", "b"}, []string{"INT", "INT", "INT"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"1", "10", "3"}, {"2", "20", "1"}} {
		if err := store.Insert("app", "issue251", row); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeStatement(executor, "SELECT id FROM issue251 ORDER BY a + b")
	if err != nil {
		t.Fatalf("ORDER BY source expression: %v", err)
	}
	if want := [][]string{{"1"}, {"2"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY a + b rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT a + b AS total FROM issue251 ORDER BY total")
	if err != nil {
		t.Fatalf("ORDER BY bare alias: %v", err)
	}
	if want := [][]string{{"13"}, {"21"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY bare alias rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT a + b AS total FROM issue251 ORDER BY total + 1")
	if err != nil {
		t.Fatalf("ORDER BY alias in expression: %v", err)
	}
	if want := [][]string{{"13"}, {"21"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY alias in expression rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT id, a + b AS total FROM issue251 ORDER BY total + 1 DESC")
	if err != nil {
		t.Fatalf("ORDER BY alias in expression DESC: %v", err)
	}
	if want := [][]string{{"2", "21"}, {"1", "13"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY alias in expression DESC rows = %#v, want %#v", result.rows, want)
	}
}

func TestIssue251OrderResolvesAliasesAcrossProjectionKinds(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue251_kinds", []string{"id", "a", "b"}, []string{"INT", "INT", "INT"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"1", "10", "3"}, {"2", "20", "1"}, {"3", "30", "2"}} {
		if err := store.Insert("app", "issue251_kinds", row); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeStatement(executor, "SELECT b, SUM(a) AS s FROM issue251_kinds GROUP BY b ORDER BY s + 1 DESC")
	if err != nil {
		t.Fatalf("ORDER BY aggregate alias in expression: %v", err)
	}
	if want := [][]string{{"2", "30"}, {"1", "20"}, {"3", "10"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY aggregate alias in expression rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT id, RANK() OVER (ORDER BY a) AS r FROM issue251_kinds ORDER BY r + 1 DESC")
	if err != nil {
		t.Fatalf("ORDER BY window alias in expression: %v", err)
	}
	if want := [][]string{{"3", "3"}, {"2", "2"}, {"1", "1"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY window alias in expression rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT id, RANK() OVER (ORDER BY a) AS r FROM issue251_kinds ORDER BY id + 1 DESC")
	if err != nil {
		t.Fatalf("ORDER BY expression with window projection: %v", err)
	}
	if want := [][]string{{"3", "3"}, {"2", "2"}, {"1", "1"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY expression with window projection rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT id, b AS a FROM issue251_kinds ORDER BY a + 1")
	if err != nil {
		t.Fatalf("ORDER BY alias shadowing a column in expression: %v", err)
	}
	if want := [][]string{{"2", "1"}, {"3", "2"}, {"1", "3"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY alias shadowing a column rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT id, b AS a FROM issue251_kinds ORDER BY a DESC")
	if err != nil {
		t.Fatalf("ORDER BY shadowing alias: %v", err)
	}
	if want := [][]string{{"1", "3"}, {"3", "2"}, {"2", "1"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY shadowing alias rows = %#v, want %#v", result.rows, want)
	}
}

func TestIssue251UnknownIdentifierInOrderExpressionStillRejected(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue251_unknown", []string{"id", "a"}, []string{"INT", "INT"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "issue251_unknown", []string{"1", "10"}); err != nil {
		t.Fatal(err)
	}

	_, err := executeStatement(executor, "SELECT a AS x FROM issue251_unknown ORDER BY x + unknown_col")
	if err == nil || !strings.Contains(err.Error(), "Unknown column 'x + unknown_col' in 'order clause'") {
		t.Fatalf("ORDER BY x + unknown_col error = %v, want unknown column in 'order clause'", err)
	}
}
