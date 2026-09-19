package mysql

import "testing"

// Issue #408: the aggregate and window scanners inspected characters inside
// quoted string literals. A literal such as 'count(*)' or
// 'ROW_NUMBER() OVER ()' was parsed as an aggregate or window call in the
// select list, HAVING, and ORDER BY.
func TestIssue408QuotedLiteralsAreNotAggregatesOrWindows(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE t408 (id INT PRIMARY KEY, name VARCHAR(20))",
		"INSERT INTO t408 VALUES (1, 'count(*)'), (2, 'other'), (3, '1')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}
	cases := []struct {
		query string
		want  [][]string
	}{
		{"SELECT 'count(*)' FROM t408 ORDER BY id", [][]string{{"count(*)"}, {"count(*)"}, {"count(*)"}}},
		{"SELECT 'count(nonexistent)' FROM t408 ORDER BY id", [][]string{{"count(nonexistent)"}, {"count(nonexistent)"}, {"count(nonexistent)"}}},
		{`SELECT "sum(id)" FROM t408 ORDER BY id`, [][]string{{"sum(id)"}, {"sum(id)"}, {"sum(id)"}}},
		{"SELECT CONCAT('max(', name, ')') FROM t408 ORDER BY id", [][]string{{"max(count(*))"}, {"max(other)"}, {"max(1)"}}},
		{"SELECT 'it''s count(*)' FROM t408 WHERE id = 1", [][]string{{"it's count(*)"}}},
		{"SELECT COUNT(*), 'count(*)' FROM t408", [][]string{{"3", "count(*)"}}},
		{"SELECT COUNT(*) + 0, 'sum(nonexistent)' FROM t408", [][]string{{"3", "sum(nonexistent)"}}},
		{"SELECT name FROM t408 GROUP BY name HAVING name = 'count(*)'", [][]string{{"count(*)"}}},
		{"SELECT name FROM t408 GROUP BY name HAVING name = 'sum(1)'", nil},
		{"SELECT name FROM t408 GROUP BY name HAVING COUNT(*) = 1 AND name = 'count(*)'", [][]string{{"count(*)"}}},
		{"SELECT name FROM t408 GROUP BY name ORDER BY (name = 'count(nonexistent)'), name", [][]string{{"1"}, {"count(*)"}, {"other"}}},
		{"SELECT name FROM t408 GROUP BY name ORDER BY (name = 'count(*)') DESC, name", [][]string{{"count(*)"}, {"1"}, {"other"}}},
		{"SELECT 'ROW_NUMBER() OVER ()' FROM t408 ORDER BY id", [][]string{{"ROW_NUMBER() OVER ()"}, {"ROW_NUMBER() OVER ()"}, {"ROW_NUMBER() OVER ()"}}},
		{"SELECT ROW_NUMBER() OVER (ORDER BY id) + 0, 'rank() over ()' FROM t408 ORDER BY id", [][]string{{"1", "rank() over ()"}, {"2", "rank() over ()"}, {"3", "rank() over ()"}}},
	}
	for _, tc := range cases {
		result, err := executeStatement(executor, tc.query)
		if err != nil {
			t.Errorf("execute(%q): %v", tc.query, err)
			continue
		}
		if !equalRows(result.rows, tc.want) {
			t.Errorf("execute(%q) rows = %#v, want %#v", tc.query, result.rows, tc.want)
		}
	}
}
