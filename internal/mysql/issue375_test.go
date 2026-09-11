package mysql

import "testing"

func TestIssue375BinaryLocateValues(t *testing.T) {
	executor := expressionExecutor(t)
	cases := map[string]string{
		"SELECT LOCATE(CAST('a' AS BINARY), 'A')":                      "0",
		"SELECT LOCATE('a', CAST('A' AS BINARY))":                      "0",
		"SELECT LOCATE(CAST('a' AS BINARY), CAST('A' AS BINARY))":      "0",
		"SELECT LOCATE('b', CAST('🔥b' AS BINARY))":                     "5",
		"SELECT LOCATE(CAST('b' AS BINARY), '🔥b')":                     "5",
		"SELECT LOCATE('b', CAST('🔥b' AS BINARY), 5)":                  "5",
		"SELECT LOCATE('b', '🔥b', 2)":                                  "2",
		"SELECT LOCATE(CAST('' AS BINARY), CAST('abc' AS BINARY))":     "1",
		"SELECT LOCATE(CAST('' AS BINARY), CAST('abc' AS BINARY), 3)":  "3",
		"SELECT LOCATE(CAST('' AS BINARY), CAST('abc' AS BINARY), 4)":  "4",
		"SELECT LOCATE(CAST('' AS BINARY), CAST('abc' AS BINARY), 5)":  "0",
		"SELECT LOCATE(CAST('a' AS BINARY), CAST('abc' AS BINARY), 0)": "0",
		"SELECT LOCATE('B', 'abc')":                                    "2",
	}
	for query, want := range cases {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
		if !equalRows(result.rows, [][]string{{want}}) {
			t.Errorf("execute(%q) rows = %#v, want [[%s]]", query, result.rows, want)
		}
	}
}

func TestIssue375BinaryLocateInPredicates(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE bin_locate (id INT PRIMARY KEY, val VARBINARY(16))",
		"INSERT INTO bin_locate VALUES (1, CAST('Alpha' AS BINARY))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}

	for _, query := range []string{
		"SELECT id FROM bin_locate WHERE LOCATE('alpha', val) > 0",
		"SELECT id FROM bin_locate WHERE LOCATE(val, 'alpha') > 0",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
		if len(result.rows) != 0 {
			t.Errorf("execute(%q) rows = %#v, want no rows", query, result.rows)
		}
	}
}

func TestIssue375BinaryLocatePropagatesNull(t *testing.T) {
	executor := expressionExecutor(t)
	result, err := executeStatement(executor, "SELECT LOCATE(NULL, CAST('abc' AS BINARY))")
	if err != nil {
		t.Fatalf("execute NULL LOCATE: %v", err)
	}
	if len(result.rows) != 1 || len(result.nulls) != 1 || !result.nulls[0][0] {
		t.Fatalf("NULL LOCATE result = rows %#v nulls %#v, want one NULL row", result.rows, result.nulls)
	}
}
