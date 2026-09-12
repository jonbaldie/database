package mysql

import "testing"

func TestIssue382UpdateAdvancesAutoIncrement(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY AUTO_INCREMENT, val VARCHAR(20))",
		"INSERT INTO t (val) VALUES ('first')",
		"UPDATE t SET id = 5 WHERE val = 'first'",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT AUTO_INCREMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 't'")
	if err != nil {
		t.Fatalf("select auto increment: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"6"}}) {
		t.Fatalf("TABLES.AUTO_INCREMENT = %#v, want 6", result.rows)
	}

	if _, err := executeStatement(executor, "INSERT INTO t (val) VALUES ('second')"); err != nil {
		t.Fatalf("insert after update: %v", err)
	}
	result, err = executeStatement(executor, "SELECT id, val FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"5", "first"}, {"6", "second"}}) {
		t.Fatalf("rows = %#v", result.rows)
	}
}

func TestIssue382UpdateBelowCounterDoesNotRewind(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY AUTO_INCREMENT, val VARCHAR(20))",
		"INSERT INTO t (val) VALUES ('first')",
		"INSERT INTO t (id, val) VALUES (7, 'seventh')",
		"UPDATE t SET id = 3 WHERE val = 'first'",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT AUTO_INCREMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 't'")
	if err != nil {
		t.Fatalf("select auto increment: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"8"}}) {
		t.Fatalf("TABLES.AUTO_INCREMENT = %#v, want 8", result.rows)
	}
}
