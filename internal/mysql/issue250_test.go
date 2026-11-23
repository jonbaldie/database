package mysql

import (
	"reflect"
	"strings"
	"testing"
)

func TestIssue250NegativeIntegerLiteralsAreConstantExpressions(t *testing.T) {
	executor := relationalSelectExecutor(t)
	seedSQL(t, executor, "CREATE TABLE issue250 (id INT, a INT, b INT)", "INSERT INTO issue250 VALUES (1, 10, 3), (2, 20, 1), (3, 10, 3)")

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
	seedSQL(t, executor, "CREATE TABLE issue250_ordinal (id INT, a INT)", "INSERT INTO issue250_ordinal VALUES (1, 10)")

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
