package mysql

import "testing"

func TestIssue417UniqueIndexValidatesExistingRows(t *testing.T) {
	cases := []struct {
		name   string
		setup  []string
		create string
		insert string
	}{
		{
			name:   "CREATE UNIQUE INDEX",
			setup:  []string{"CREATE TABLE t (id INT PRIMARY KEY, val INT)", "INSERT INTO t VALUES (1, 10), (2, 10)"},
			create: "CREATE UNIQUE INDEX idx_val ON t (val)",
			insert: "INSERT INTO t VALUES (3, 10)",
		},
		{
			name:   "functional CREATE UNIQUE INDEX",
			setup:  []string{"CREATE TABLE t (id INT PRIMARY KEY, val VARCHAR(20))", "INSERT INTO t VALUES (1, 'abc'), (2, 'ABC')"},
			create: "CREATE UNIQUE INDEX idx_val ON t ((LOWER(val)))",
			insert: "INSERT INTO t VALUES (3, 'Abc')",
		},
		{
			name:   "ALTER TABLE ADD UNIQUE INDEX",
			setup:  []string{"CREATE TABLE t (id INT PRIMARY KEY, val INT)", "INSERT INTO t VALUES (1, 10), (2, 10)"},
			create: "ALTER TABLE t ADD UNIQUE INDEX idx_val (val)",
			insert: "INSERT INTO t VALUES (3, 10)",
		},
		{
			name:   "replace non-unique index with unique index of the same name",
			setup:  []string{"CREATE TABLE t (id INT PRIMARY KEY, val INT, INDEX idx_val (val))", "INSERT INTO t VALUES (1, 10), (2, 10)"},
			create: "ALTER TABLE t DROP INDEX idx_val, ADD UNIQUE INDEX idx_val (val)",
			insert: "INSERT INTO t VALUES (3, 10)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range tc.setup {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("setup %q: %v", query, err)
				}
			}
			if _, err := executeStatement(executor, tc.create); !isFailureCode(err, 1062) {
				t.Fatalf("%q: expected duplicate key error 1062, got %v", tc.create, err)
			}
			indexes, err := executeStatement(executor, "SHOW INDEX FROM t WHERE Key_name = 'idx_val'")
			if err != nil {
				t.Fatalf("SHOW INDEX: %v", err)
			}
			for _, row := range indexes.rows {
				if row[1] == "0" {
					t.Fatalf("unique idx_val exists after rejected DDL: %v", indexes.rows)
				}
			}
			if _, err := executeStatement(executor, tc.insert); err != nil {
				t.Fatalf("INSERT after rejected DDL: %v", err)
			}
		})
	}
}
