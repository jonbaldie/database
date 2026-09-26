package mysql

import "testing"

// TestIssue448LastInsertIDFollowsMySQLInsertIDRules checks the OK-packet last
// insert ID against the MySQL 8.4 mysql_insert_id() rules: the first generated
// value that the statement stores, or otherwise the AUTO_INCREMENT value of the
// last row that it inserted or updated, or otherwise 0.
func TestIssue448LastInsertIDFollowsMySQLInsertIDRules(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(10) UNIQUE, n INT)",
		"CREATE TABLE source (code VARCHAR(10))",
		"INSERT INTO source (code) VALUES ('s1'), ('s2')",
		"CREATE TABLE explicit_source (id INT, code VARCHAR(10))",
		"INSERT INTO explicit_source (id, code) VALUES (40, 'x'), (41, 'y')",
		"CREATE TABLE plain (id INT PRIMARY KEY)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	for _, tc := range []struct {
		query        string
		wantAffected uint64
		wantID       uint64
	}{
		{"INSERT INTO t (code) VALUES ('a')", 1, 1},
		{"INSERT INTO t (id, code) VALUES (10, 'b')", 1, 10},
		{"INSERT INTO t (id, code) VALUES (20, 'c'), (NULL, 'd')", 2, 21},
		{"INSERT INTO t (id, code) VALUES (NULL, 'e'), (30, 'f')", 2, 22},
		{"INSERT INTO t (code) SELECT code FROM source ORDER BY code", 2, 31},
		{"INSERT INTO t (id, code) SELECT id, code FROM explicit_source ORDER BY id", 2, 41},
		{"REPLACE INTO t (id, code) VALUES (1, 'a2')", 2, 1},
		{"REPLACE INTO t (code) VALUES ('r')", 1, 42},
		{"INSERT INTO t (code, n) VALUES ('b', 5) ON DUPLICATE KEY UPDATE n = VALUES(n)", 2, 10},
		{"INSERT INTO t (code, n) VALUES ('b', 5) ON DUPLICATE KEY UPDATE n = VALUES(n)", 0, 0},
		{"INSERT INTO t (code, n) VALUES ('b', 6), ('u', 1) ON DUPLICATE KEY UPDATE n = VALUES(n)", 3, 46},
		{"UPDATE t SET n = 7 WHERE id = 1", 1, 0},
		{"DELETE FROM t WHERE id = 46", 1, 0},
		{"INSERT INTO plain (id) VALUES (5)", 1, 0},
	} {
		result, err := executeStatement(executor, tc.query)
		if err != nil {
			t.Fatalf("execute %q: %v", tc.query, err)
		}
		if result.affected != tc.wantAffected || result.lastInsertID != tc.wantID {
			t.Fatalf("%s: affected %d, last insert ID %d; want %d, %d", tc.query, result.affected, result.lastInsertID, tc.wantAffected, tc.wantID)
		}
	}
}

// TestIssue448FailedMultiRowInsertStoresNothing proves that a multi-row insert
// that fails partway returns an error, not an OK packet, and stores no row.
// The next insert reports the id that it stored.
func TestIssue448FailedMultiRowInsertStoresNothing(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(10) UNIQUE)",
		"INSERT INTO t (code) VALUES ('a')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "INSERT INTO t (code) VALUES ('b'), ('a')"); err == nil {
		t.Fatal("duplicate multi-row insert succeeded")
	}
	result, err := executeStatement(executor, "INSERT INTO t (code) VALUES ('c')")
	if err != nil {
		t.Fatalf("insert after failure: %v", err)
	}
	rows, err := executeStatement(executor, "SELECT id, code FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows.rows) != 2 || rows.rows[1][1] != "c" {
		t.Fatalf("rows = %#v, want the rows 'a' and 'c' only", rows.rows)
	}
	if got := rows.rows[1][0]; got != "2" || result.lastInsertID != 2 {
		t.Fatalf("stored id %s, last insert ID %d; want 2, 2", got, result.lastInsertID)
	}
}
