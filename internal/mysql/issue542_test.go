package mysql

import "testing"

func TestIssue542InformationSchemaIntegerColumnsSortNumerically(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE wide (c01 INT, c02 INT, c03 INT, c04 INT, c05 INT, c06 INT, c07 INT, c08 INT, c09 INT, c10 INT, c11 INT, c12 INT)",
		"CREATE INDEX wide_index ON wide (c01, c02, c03, c04, c05, c06, c07, c08, c09, c10, c11)",
		"CREATE TABLE p (id INT PRIMARY KEY)",
		"INSERT INTO p VALUES (10), (2)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	ascending := [][]string{{"c01"}, {"c02"}, {"c03"}, {"c04"}, {"c05"}, {"c06"}, {"c07"}, {"c08"}, {"c09"}, {"c10"}, {"c11"}, {"c12"}}
	descending := make([][]string, len(ascending))
	for index := range ascending {
		descending[index] = ascending[len(ascending)-1-index]
	}
	const columns = "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 'wide' "
	for _, test := range []struct {
		query string
		rows  [][]string
	}{
		{query: columns + "ORDER BY ORDINAL_POSITION", rows: ascending},
		{query: columns + "ORDER BY ORDINAL_POSITION DESC", rows: descending},
		{query: columns + "ORDER BY ORDINAL_POSITION + 0", rows: ascending},
		{query: columns + "AND ORDINAL_POSITION > 2 AND ORDINAL_POSITION < 11 ORDER BY ORDINAL_POSITION", rows: ascending[2:10]},
		{
			query: "SELECT COLUMN_NAME FROM information_schema.STATISTICS WHERE TABLE_NAME = 'wide' ORDER BY SEQ_IN_INDEX DESC",
			rows:  [][]string{{"c11"}, {"c10"}, {"c09"}, {"c08"}, {"c07"}, {"c06"}, {"c05"}, {"c04"}, {"c03"}, {"c02"}, {"c01"}},
		},
		{query: "SELECT id FROM p ORDER BY id + 0", rows: [][]string{{"2"}, {"10"}}},
	} {
		result, err := executeStatement(executor, test.query)
		if err != nil || !equalRows(result.rows, test.rows) {
			t.Fatalf("%s = %#v, %v; want %#v", test.query, result.rows, err, test.rows)
		}
	}

	for _, query := range []string{
		"SELECT ORDINAL_POSITION FROM information_schema.COLUMNS WHERE TABLE_NAME = 'wide' ORDER BY ORDINAL_POSITION",
		"SELECT ORDINAL_POSITION FROM information_schema.COLUMNS",
	} {
		result, err := executeStatement(executor, query)
		if err != nil || len(result.metadata) != 1 || result.metadata[0].typ != mysqlTypeLong {
			t.Fatalf("%s metadata = %#v, %v; want INT", query, result.metadata, err)
		}
	}

	if _, err := executeStatement(executor, columns+"ORDER BY NO_SUCH_COLUMN + 0"); err == nil || err.Error() != "Unknown column 'NO_SUCH_COLUMN + 0' in 'order clause'" {
		t.Fatalf("unknown order column error = %v", err)
	}
}
