package mysql

import "testing"

func TestIssue383BinarySubstringValues(t *testing.T) {
	executor := expressionExecutor(t)
	cases := []struct {
		query string
		want  string
	}{
		{"SELECT SUBSTRING('🔥b', 2, 1)", "b"},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 2, 1)", string([]byte{0x9f})},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 5, 1)", "b"},
		{"SELECT LENGTH(SUBSTRING(CAST('🔥b' AS BINARY), 1, 1))", "1"},
		{"SELECT LENGTH(SUBSTRING(CAST('🔥b' AS BINARY), -2, 1))", "1"},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 2)", string([]byte{0x9f, 0x94, 0xa5, 'b'})},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY) FROM 2 FOR 1)", string([]byte{0x9f})},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY) FROM -2)", string([]byte{0xa5, 'b'})},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 0)", ""},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 6)", ""},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), -10)", ""},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 1, 0)", ""},
		{"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 1, -1)", ""},
	}
	for _, tc := range cases {
		result, err := executeStatement(executor, tc.query)
		if err != nil {
			t.Fatalf("execute(%q): %v", tc.query, err)
		}
		if !equalRows(result.rows, [][]string{{tc.want}}) {
			t.Errorf("execute(%q) rows = %#v, want [[%q]]", tc.query, result.rows, tc.want)
		}
	}
}

func TestIssue383BinarySubstringInTable(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE bin_sub (id INT PRIMARY KEY, val VARBINARY(16))",
		"INSERT INTO bin_sub VALUES (1, CAST('🔥b' AS BINARY))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT id, SUBSTRING(val, 5, 1), LENGTH(SUBSTRING(val, 1, 1)) FROM bin_sub")
	if err != nil {
		t.Fatalf("execute table query: %v", err)
	}
	want := [][]string{{"1", "b", "1"}}
	if !equalRows(result.rows, want) {
		t.Errorf("table query rows = %#v, want %#v", result.rows, want)
	}
}

func TestIssue383BinarySubstringPropagatesNull(t *testing.T) {
	executor := expressionExecutor(t)
	cases := []string{
		"SELECT SUBSTRING(NULL, 1, 1)",
		"SELECT SUBSTRING(CAST('🔥b' AS BINARY), NULL, 1)",
		"SELECT SUBSTRING(CAST('🔥b' AS BINARY), 1, NULL)",
	}
	for _, query := range cases {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
		if len(result.rows) != 1 || len(result.nulls) != 1 || !result.nulls[0][0] {
			t.Fatalf("execute(%q) = rows %#v nulls %#v, want one NULL row", query, result.rows, result.nulls)
		}
	}
}
