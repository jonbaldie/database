package mysql

import (
	"reflect"
	"testing"
)

func TestIssue418IndexHintsAcceptForGroupByScope(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, a INT, KEY idx_a (a))",
		"INSERT INTO t VALUES (1, 10), (2, 20)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	cases := []struct {
		query string
		want  [][]string
	}{
		{"SELECT a FROM t USE INDEX FOR JOIN (idx_a) ORDER BY a", [][]string{{"10"}, {"20"}}},
		{"SELECT a FROM t USE INDEX FOR ORDER BY (idx_a) ORDER BY a", [][]string{{"10"}, {"20"}}},
		{"SELECT a FROM t USE INDEX FOR GROUP BY (idx_a) ORDER BY a", [][]string{{"10"}, {"20"}}},
		{"SELECT a FROM t FORCE INDEX FOR GROUP BY (idx_a) ORDER BY a", [][]string{{"10"}, {"20"}}},
		{"SELECT a FROM t IGNORE INDEX FOR GROUP BY (idx_a) ORDER BY a", [][]string{{"10"}, {"20"}}},
		{"SELECT a, COUNT(*) FROM t USE INDEX FOR GROUP BY (idx_a) GROUP BY a ORDER BY a", [][]string{{"10", "1"}, {"20", "1"}}},
	}
	for _, tc := range cases {
		result, err := executeStatement(executor, tc.query)
		if err != nil {
			t.Errorf("execute(%q): %v", tc.query, err)
			continue
		}
		if !reflect.DeepEqual(result.rows, tc.want) {
			t.Errorf("execute(%q) rows = %#v, want %#v", tc.query, result.rows, tc.want)
		}
	}
}
