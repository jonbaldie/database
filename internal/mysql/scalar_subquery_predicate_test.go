package mysql

import "testing"

func TestScalarSubqueryComparisonOperand(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE values_table (id INT PRIMARY KEY, value INT)",
		"INSERT INTO values_table VALUES (1, 10), (2, 20), (3, 30)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM values_table WHERE value = (SELECT MAX(value) FROM values_table)")
	if err != nil {
		t.Fatalf("scalar subquery comparison: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"3"}}) {
		t.Fatalf("scalar subquery comparison = %#v, want [[3]]", result.rows)
	}
}
