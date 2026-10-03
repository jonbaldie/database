package mysql

import "testing"

func TestIssue485ConstraintAndStatisticsViewsUseMySQLColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pid INT, CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES p (id), CONSTRAINT ck1 CHECK (id > 0))",
		"CREATE INDEX ic ON c (pid)",
		"INSERT INTO c VALUES (1, NULL), (2, NULL)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	for _, test := range []struct {
		view    string
		columns []string
	}{
		{view: "TABLE_CONSTRAINTS", columns: []string{
			"CONSTRAINT_CATALOG", "CONSTRAINT_SCHEMA", "CONSTRAINT_NAME", "TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_TYPE", "ENFORCED",
		}},
		{view: "REFERENTIAL_CONSTRAINTS", columns: []string{
			"CONSTRAINT_CATALOG", "CONSTRAINT_SCHEMA", "CONSTRAINT_NAME", "UNIQUE_CONSTRAINT_CATALOG", "UNIQUE_CONSTRAINT_SCHEMA",
			"UNIQUE_CONSTRAINT_NAME", "MATCH_OPTION", "UPDATE_RULE", "DELETE_RULE", "TABLE_NAME", "REFERENCED_TABLE_NAME",
		}},
		{view: "KEY_COLUMN_USAGE", columns: []string{
			"CONSTRAINT_CATALOG", "CONSTRAINT_SCHEMA", "CONSTRAINT_NAME", "TABLE_CATALOG", "TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME",
			"ORDINAL_POSITION", "POSITION_IN_UNIQUE_CONSTRAINT", "REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME", "REFERENCED_COLUMN_NAME",
		}},
		{view: "STATISTICS", columns: []string{
			"TABLE_CATALOG", "TABLE_SCHEMA", "TABLE_NAME", "NON_UNIQUE", "INDEX_SCHEMA", "INDEX_NAME", "SEQ_IN_INDEX", "COLUMN_NAME",
			"COLLATION", "CARDINALITY", "SUB_PART", "PACKED", "NULLABLE", "INDEX_TYPE", "COMMENT", "INDEX_COMMENT", "IS_VISIBLE", "EXPRESSION",
		}},
		{view: "CHECK_CONSTRAINTS", columns: []string{"CONSTRAINT_CATALOG", "CONSTRAINT_SCHEMA", "CONSTRAINT_NAME", "CHECK_CLAUSE"}},
	} {
		result, err := executeStatement(executor, "SELECT * FROM information_schema."+test.view+" LIMIT 0")
		if err != nil || !equalStrings(result.columns, test.columns) {
			t.Fatalf("%s columns = %#v, %v; want %#v", test.view, result, err, test.columns)
		}
	}

	for _, test := range []struct {
		query string
		rows  [][]string
		nulls [][]bool
	}{
		{
			query: "SELECT CONSTRAINT_CATALOG, CONSTRAINT_NAME, ENFORCED FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_NAME = 'c' ORDER BY CONSTRAINT_NAME",
			rows:  [][]string{{"def", "ck1", "YES"}, {"def", "fk1", "YES"}, {"def", "PRIMARY", "YES"}},
		},
		{
			query: "SELECT UNIQUE_CONSTRAINT_CATALOG, UNIQUE_CONSTRAINT_SCHEMA, UNIQUE_CONSTRAINT_NAME, MATCH_OPTION, UPDATE_RULE, DELETE_RULE FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE TABLE_NAME = 'c'",
			rows:  [][]string{{"def", "app", "PRIMARY", "NONE", "NO ACTION", "NO ACTION"}},
		},
		{
			query: "SELECT CONSTRAINT_NAME, TABLE_CATALOG, POSITION_IN_UNIQUE_CONSTRAINT FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_NAME = 'c' ORDER BY CONSTRAINT_NAME",
			rows:  [][]string{{"fk1", "def", "1"}, {"PRIMARY", "def", "NULL"}},
			nulls: [][]bool{{false, false, false}, {false, false, true}},
		},
		{
			query: "SELECT INDEX_NAME, TABLE_CATALOG, INDEX_SCHEMA, CARDINALITY, PACKED, IS_VISIBLE, EXPRESSION FROM information_schema.STATISTICS WHERE TABLE_NAME = 'c' ORDER BY INDEX_NAME",
			rows:  [][]string{{"ic", "def", "app", "2", "NULL", "YES", "NULL"}, {"PRIMARY", "def", "app", "2", "NULL", "YES", "NULL"}},
			nulls: [][]bool{{false, false, false, false, true, false, true}, {false, false, false, false, true, false, true}},
		},
	} {
		result, err := executeStatement(executor, test.query)
		if err != nil || !equalRows(result.rows, test.rows) {
			t.Fatalf("%s = %#v, %v; want %#v", test.query, result, err, test.rows)
		}
		if test.nulls != nil && !equalNulls(result.nulls, test.nulls) {
			t.Fatalf("%s nulls = %#v; want %#v", test.query, result.nulls, test.nulls)
		}
	}
}

func equalNulls(got, want [][]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for row := range want {
		if len(got[row]) != len(want[row]) {
			return false
		}
		for column := range want[row] {
			if got[row][column] != want[row][column] {
				return false
			}
		}
	}
	return true
}
