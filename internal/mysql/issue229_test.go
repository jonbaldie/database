package mysql

import "testing"

func TestIssue229DeleteOrderByLimit(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, value INT)",
		"INSERT INTO items VALUES (1, 30), (2, 10), (3, 20), (4, 40), (5, 50)",
		"DELETE FROM items ORDER BY value LIMIT 2",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	assertIssue229Rows(t, executor, [][]string{{"1", "30"}, {"4", "40"}, {"5", "50"}})

	if _, err := executeStatement(executor, "DELETE FROM items WHERE value >= 30 ORDER BY value DESC LIMIT 1"); err != nil {
		t.Fatalf("delete with WHERE, ORDER BY, and LIMIT: %v", err)
	}
	assertIssue229Rows(t, executor, [][]string{{"1", "30"}, {"4", "40"}})

	for _, query := range []string{
		"DELETE FROM items ORDER BY value LIMIT 0",
		"DELETE FROM items WHERE value = 999 ORDER BY value LIMIT 2",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute no-op %q: %v", query, err)
		}
	}
	assertIssue229Rows(t, executor, [][]string{{"1", "30"}, {"4", "40"}})
}

func assertIssue229Rows(t *testing.T, executor *textStatementExecutor, want [][]string) {
	t.Helper()
	result, err := executeStatement(executor, "SELECT id, value FROM items ORDER BY id")
	if err != nil {
		t.Fatalf("read items: %v", err)
	}
	if !equalRows(result.rows, want) {
		t.Fatalf("items = %#v, want %#v", result.rows, want)
	}
}
