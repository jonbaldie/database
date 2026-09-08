package mysql

import "testing"

func TestIssue317ExistsWithoutFromClause(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t1 (id INT PRIMARY KEY)",
		"INSERT INTO t1 VALUES (1), (2)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	// 1. EXISTS with false condition returns 0 rows
	res, err := executeStatement(executor, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 WHERE 1 = 0)")
	if err != nil {
		t.Fatalf("execute EXISTS false: %v", err)
	}
	if len(res.rows) != 0 {
		t.Fatalf("expected 0 rows, got %d rows: %#v", len(res.rows), res.rows)
	}

	// 2. NOT EXISTS with false condition returns all rows
	res, err = executeStatement(executor, "SELECT id FROM t1 WHERE NOT EXISTS (SELECT 1 WHERE 1 = 0) ORDER BY id")
	if err != nil {
		t.Fatalf("execute NOT EXISTS false: %v", err)
	}
	if !equalRows(res.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("expected [[1], [2]], got %#v", res.rows)
	}

	// 3. EXISTS with true condition returns all rows
	res, err = executeStatement(executor, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 WHERE 1 = 1) ORDER BY id")
	if err != nil {
		t.Fatalf("execute EXISTS true: %v", err)
	}
	if !equalRows(res.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("expected [[1], [2]], got %#v", res.rows)
	}

	// 4. NOT EXISTS with true condition returns 0 rows
	res, err = executeStatement(executor, "SELECT id FROM t1 WHERE NOT EXISTS (SELECT 1 WHERE 1 = 1)")
	if err != nil {
		t.Fatalf("execute NOT EXISTS true: %v", err)
	}
	if len(res.rows) != 0 {
		t.Fatalf("expected 0 rows, got %d rows: %#v", len(res.rows), res.rows)
	}

	// 5. Correlated subquery without FROM clause
	res, err = executeStatement(executor, "SELECT id FROM t1 WHERE EXISTS (SELECT 1 WHERE t1.id = 1)")
	if err != nil {
		t.Fatalf("execute correlated EXISTS: %v", err)
	}
	if !equalRows(res.rows, [][]string{{"1"}}) {
		t.Fatalf("expected [[1]], got %#v", res.rows)
	}

	// 6. Standalone scalar SELECT with WHERE
	res, err = executeStatement(executor, "SELECT 1 WHERE 1 = 0")
	if err != nil {
		t.Fatalf("execute standalone false: %v", err)
	}
	if len(res.rows) != 0 {
		t.Fatalf("expected 0 rows, got %d rows: %#v", len(res.rows), res.rows)
	}

	res, err = executeStatement(executor, "SELECT 1 WHERE 1 = 1")
	if err != nil {
		t.Fatalf("execute standalone true: %v", err)
	}
	if !equalRows(res.rows, [][]string{{"1"}}) {
		t.Fatalf("expected [[1]], got %#v", res.rows)
	}
}
