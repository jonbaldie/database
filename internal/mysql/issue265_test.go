package mysql

import "testing"

func TestDropTableThenRecreateStartsEmpty(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, val INT)",
		"INSERT INTO t VALUES (1, 100), (2, 200)",
		"DROP TABLE t",
		"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(20))",
		"INSERT INTO t VALUES (1, 'Alice')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT * FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(result.rows) != 1 || len(result.rows[0]) != 2 || result.rows[0][0] != "1" || result.rows[0][1] != "Alice" {
		t.Fatalf("rows after recreate = %v", result.rows)
	}
}
