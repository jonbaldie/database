package main

import "testing"

func TestReturnsRows(t *testing.T) {
	cases := []struct {
		statement string
		want      bool
	}{
		{"SELECT 1", true},
		{"  select * from t", true},
		{"SHOW TABLES", true},
		{"EXPLAIN SELECT 1", true},
		{"WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"DESCRIBE t", true},
		{"DESC t", true},
		{"INSERT INTO t VALUES (1,10),(2,20),(3,30)", false},
		{"UPDATE t SET qty = 1", false},
		{"DELETE FROM t WHERE id = 1", false},
		{"REPLACE INTO t VALUES (1,99)", false},
		{"CREATE TABLE t (id INT PRIMARY KEY)", false},
		{"INSERT INTO t VALUES (1,1) ON DUPLICATE KEY UPDATE qty = qty", false},
	}
	for _, c := range cases {
		if got := returnsRows(c.statement); got != c.want {
			t.Errorf("returnsRows(%q) = %v, want %v", c.statement, got, c.want)
		}
	}
}
