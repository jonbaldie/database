package mysql

import "testing"

// TestShowCatalogClauses covers issue 400: the FROM/IN, LIKE, and WHERE forms
// of the closed catalog SHOW surface.
func TestShowCatalogClauses(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"CREATE DATABASE odku", "CREATE TABLE odku.t (id INT PRIMARY KEY, q INT, INDEX q_index (q))", "USE odku"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	cases := []struct {
		query   string
		columns int
		rows    [][]string
	}{
		{"SHOW COLUMNS FROM t FROM odku LIKE 'q'", 6, [][]string{{"q", "INT", "YES", "MUL", "", ""}}},
		{"SHOW COLUMNS FROM t WHERE Field = 'id'", 6, [][]string{{"id", "INT", "NO", "PRI", "", ""}}},
		{"DESCRIBE t q", 6, [][]string{{"q", "INT", "YES", "MUL", "", ""}}},
		{"SHOW TABLES FROM odku", 1, [][]string{{"t"}}},
		{"SHOW TABLES IN odku LIKE 't%'", 1, [][]string{{"t"}}},
		{"SHOW FULL TABLES", 2, [][]string{{"t", "BASE TABLE"}}},
		{"SHOW FULL TABLES FROM information_schema WHERE Tables_in_information_schema = 'COLUMNS'", 2, [][]string{{"columns", "SYSTEM VIEW"}}},
		{"SHOW TABLES WHERE Tables_in_odku = 'nope'", 1, [][]string{}},
		{"SHOW DATABASES WHERE `Database` = 'odku'", 1, [][]string{{"odku"}}},
		{"SHOW INDEX FROM t FROM odku WHERE Key_name = 'PRIMARY'", 15, nil},
		{"SHOW INDEX FROM t WHERE Non_unique = 1", 15, nil},
		{"SHOW CHARACTER SET LIKE 'utf%'", 4, [][]string{{"utf8mb4", "UTF-8 Unicode", "utf8mb4_0900_ai_ci", "4"}}},
		{"SHOW COLLATION WHERE Charset = 'utf8mb4' AND Id = 46", 7, [][]string{{"utf8mb4_bin", "utf8mb4", "46", "", "Yes", "1", "PAD SPACE"}}},
	}
	for _, c := range cases {
		result, err := executeStatement(executor, c.query)
		if err != nil {
			t.Errorf("%s: %v", c.query, err)
			continue
		}
		if len(result.columns) != c.columns {
			t.Errorf("%s: columns = %v", c.query, result.columns)
		}
		if c.rows != nil && !equalRows(result.rows, c.rows) {
			t.Errorf("%s: rows = %#v, want %#v", c.query, result.rows, c.rows)
		}
		if result.nulls != nil && len(result.nulls) != len(result.rows) {
			t.Errorf("%s: nulls misaligned with rows", c.query)
		}
	}
	if result, _ := executeStatement(executor, "SHOW INDEX FROM t WHERE Key_name = 'PRIMARY'"); result == nil || len(result.rows) != 1 {
		t.Errorf("index where = %#v", result)
	}
	if _, err := executeStatement(executor, "SHOW TABLES WHERE nope = 1"); err == nil {
		t.Error("unknown WHERE column accepted")
	}
	if _, err := executeStatement(executor, "SHOW TABLES LIKE 't' WHERE 1"); err == nil {
		t.Error("LIKE with WHERE accepted")
	}
	if _, err := executeStatement(executor, "SHOW TABLES FROM missing"); err == nil {
		t.Error("missing database accepted")
	}
}
