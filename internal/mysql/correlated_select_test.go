package mysql

import "testing"

func TestCorrelatedSubquerySeesEnclosingColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE nums (id INT, n INT)",
		"INSERT INTO nums VALUES (1, 10), (2, 20)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM nums a WHERE EXISTS (SELECT 1 FROM nums b WHERE b.id = a.id)")
	if err != nil || !equalRows(result.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("correlated EXISTS = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT id, (SELECT n FROM nums x WHERE x.id = nums.id) AS n FROM nums WHERE id = 1")
	if err != nil || !equalRows(result.rows, [][]string{{"1", "10"}}) {
		t.Fatalf("correlated projection = %#v, %v", result, err)
	}
}
