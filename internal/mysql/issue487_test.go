package mysql

import (
	"slices"
	"testing"
)

// TestIssue487ShowIndexListsPrimaryFirst checks that SHOW INDEX lists PRIMARY
// first, then unique keys, then the other keys, even when CREATE INDEX adds a
// key after the constraint-backed keys.
func TestIssue487ShowIndexListsPrimaryFirst(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pid INT, CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES p (id))",
		"CREATE INDEX ic ON c (pid)",
		"CREATE TABLE u (id INT PRIMARY KEY, a INT, b INT)",
		"CREATE INDEX ia ON u (a)",
		"CREATE UNIQUE INDEX ub ON u (b)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	for _, tc := range []struct {
		table string
		keys  []string
	}{
		{"c", []string{"PRIMARY", "ic"}},
		{"u", []string{"PRIMARY", "ub", "ia"}},
	} {
		result, err := executeStatement(executor, "SHOW INDEX FROM "+tc.table)
		if err != nil {
			t.Fatalf("SHOW INDEX FROM %s: %v", tc.table, err)
		}
		var keys []string
		for _, row := range result.rows {
			keys = append(keys, row[2])
		}
		if !slices.Equal(keys, tc.keys) {
			t.Fatalf("SHOW INDEX FROM %s keys = %v, want %v", tc.table, keys, tc.keys)
		}
	}
}
