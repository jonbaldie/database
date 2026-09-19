package mysql

import "testing"

func TestAggregateArithmeticInProjection(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE amounts (amount INT)",
		"INSERT INTO amounts VALUES (50), (70)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT MAX(amount) * 2, COUNT(*) * 2, MAX(amount) - MIN(amount), -MAX(amount) FROM amounts")
	if err != nil || !equalRows(result.rows, [][]string{{"140", "4", "20", "-70"}}) {
		t.Fatalf("aggregate arithmetic = %#v, err = %v", result.rows, err)
	}
}

func TestAggregateArithmeticOrderAndNullResults(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE amounts (id INT, amount INT)",
		"INSERT INTO amounts VALUES (1, 50), (2, 70), (3, NULL)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, MAX(amount) * 2 AS doubled FROM amounts GROUP BY id ORDER BY MAX(amount) * 2 DESC")
	if err != nil || !equalRows(result.rows, [][]string{{"2", "140"}, {"1", "100"}, {"3", "NULL"}}) {
		t.Fatalf("ordered aggregate arithmetic = %#v, err = %v", result.rows, err)
	}
}

func TestComposedAggregateStringProjectionEncodes(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY)",
		"INSERT INTO t VALUES (1)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	for _, test := range []struct {
		query string
		want  [][]string
	}{
		{query: "SELECT IF(COUNT(*) > 0, 'yes', 'no') FROM t", want: [][]string{{"yes"}}},
		{query: "SELECT CONCAT('total: ', CAST(COUNT(*) AS CHAR)) FROM t", want: [][]string{{"total: 1"}}},
		{query: "SELECT IFNULL(MAX(id), 'none') FROM t WHERE 1=0", want: [][]string{{"none"}}},
	} {
		result, err := executeStatement(executor, test.query)
		if err != nil || !equalRows(result.rows, test.want) {
			t.Fatalf("%s rows = %#v, err = %v", test.query, result.rows, err)
		}
		if len(result.metadata) != 1 || result.metadata[0].typ != mysqlTypeVarString {
			t.Fatalf("%s metadata = %#v, want VARCHAR", test.query, result.metadata)
		}
		if _, err := binaryRow(result.rows[0], 0, result.nulls, result.metadata); err != nil {
			t.Fatalf("%s binary encode: %v", test.query, err)
		}
	}
}
