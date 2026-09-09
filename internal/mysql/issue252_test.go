package mysql

import (
	"reflect"
	"strings"
	"testing"
)

// Behaviour pinned against MySQL 8.0 (docker mysql:8.0, ONLY_FULL_GROUP_BY
// default): an ORDER BY expression that exactly matches a GROUP BY expression
// is valid even when absent from the SELECT list; a superset of it is not.
func TestIssue252OrderMatchesGroupByExpression(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue252", []string{"a", "b"}, []string{"INT", "INT"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"10", "3"}, {"20", "1"}, {"10", "3"}} {
		if err := store.Insert("app", "issue252", row); err != nil {
			t.Fatal(err)
		}
	}

	result, err := executeStatement(executor, "SELECT COUNT(*) FROM issue252 GROUP BY a + b ORDER BY a + b")
	if err != nil {
		t.Fatalf("ORDER BY group expression outside SELECT list: %v", err)
	}
	if want := [][]string{{"2"}, {"1"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT a + b, COUNT(*) FROM issue252 GROUP BY a + b ORDER BY a + b")
	if err != nil {
		t.Fatalf("ORDER BY group expression in SELECT list: %v", err)
	}
	if want := [][]string{{"13", "2"}, {"21", "1"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT COUNT(*) FROM issue252 GROUP BY A + B ORDER BY a + b")
	if err != nil {
		t.Fatalf("ORDER BY group expression differing in case: %v", err)
	}
	if want := [][]string{{"2"}, {"1"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT COUNT(*) FROM issue252 GROUP BY a ORDER BY a + 1 DESC")
	if err != nil {
		t.Fatalf("ORDER BY expression over grouped column outside SELECT list: %v", err)
	}
	if want := [][]string{{"1"}, {"2"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}
}

func TestIssue252OrderStillRejectsUngroupedExpressions(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue252_bad", []string{"a", "b"}, []string{"INT", "INT"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "issue252_bad", []string{"1", "1"}); err != nil {
		t.Fatal(err)
	}

	rejected := func(query string) {
		t.Helper()
		_, err := executeStatement(executor, query)
		if err == nil {
			t.Fatalf("expected ONLY_FULL_GROUP_BY rejection, got success: %s", query)
		}
		if !strings.Contains(err.Error(), "not in GROUP BY") {
			t.Fatalf("expected ONLY_FULL_GROUP_BY rejection, got %v: %s", err, query)
		}
	}

	// MySQL 8.0 also rejects these (1055): the ORDER BY expression is not an
	// exact GROUP BY match, and the SELECT list is not grouped either.
	rejected("SELECT COUNT(*) FROM issue252_bad GROUP BY a ORDER BY a + b")
	rejected("SELECT COUNT(*) FROM issue252_bad GROUP BY a + b ORDER BY b")
	rejected("SELECT COUNT(*) FROM issue252_bad GROUP BY a + b ORDER BY a + b + 1")
	rejected("SELECT a, COUNT(*) FROM issue252_bad GROUP BY a + b ORDER BY a + b")

	// HAVING does not receive the ORDER BY exact-match leniency.
	rejected("SELECT COUNT(*) FROM issue252_bad GROUP BY a + b HAVING a + b > 0")
}
