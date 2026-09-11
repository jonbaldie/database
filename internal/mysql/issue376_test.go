package mysql

import "testing"

func TestIssue376BinaryStringFunctionValues(t *testing.T) {
	executor := expressionExecutor(t)
	cases := map[string]string{
		"SELECT REPLACE(CAST('a' AS BINARY), 'b', 'c') = 'A'":                   "0",
		"SELECT REVERSE(CAST('a' AS BINARY)) = 'A'":                             "0",
		"SELECT LTRIM(CAST(' a' AS BINARY)) = 'A'":                              "0",
		"SELECT RTRIM(CAST('a ' AS BINARY)) = 'A'":                              "0",
		"SELECT TRIM(CAST(' a ' AS BINARY)) = 'A'":                              "0",
		"SELECT UPPER(CAST('a' AS BINARY)) = CAST('a' AS BINARY)":               "1",
		"SELECT LOWER(CAST('A' AS BINARY)) = CAST('A' AS BINARY)":               "1",
		"SELECT UPPER(CAST('a' AS BINARY)) LIKE CAST('a' AS BINARY)":            "1",
		"SELECT LOWER(CAST('A' AS BINARY)) LIKE CAST('A' AS BINARY)":            "1",
		"SELECT UPPER(CAST('a' AS BINARY)) = 'A'":                               "0",
		"SELECT LOWER(CAST('A' AS BINARY)) = 'a'":                               "0",
		"SELECT CHAR_LENGTH(CAST('🔥' AS BINARY))":                               "4",
		"SELECT REVERSE(CAST('🔥' AS BINARY)) = '🔥'":                             "0",
		"SELECT REVERSE(REVERSE(CAST('🔥a' AS BINARY))) = CAST('🔥a' AS BINARY)":  "1",
		"SELECT TRIM(CAST(' a ' AS BINARY)) = CAST('a' AS BINARY)":              "1",
		"SELECT REPLACE(CAST('ab' AS BINARY), 'b', 'c') = CAST('ac' AS BINARY)": "1",
		"SELECT REPLACE(CAST('aB' AS BINARY), 'b', 'c') = CAST('aB' AS BINARY)": "1",
		"SELECT REPLACE('ab', CAST('b' AS BINARY), 'x') = CAST('ax' AS BINARY)": "1",
		"SELECT UPPER('a') = 'A'":                                               "1",
		"SELECT LOWER('A') = 'a'":                                               "1",
		"SELECT REPLACE('abc', 'b', 'x') = 'axc'":                               "1",
		"SELECT TRIM('  a  ') = 'a'":                                            "1",
		"SELECT REVERSE('🔥a') = 'a🔥'":                                           "1",
		"SELECT CHAR_LENGTH('🔥')":                                               "1",
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

func TestIssue376BinaryStringFunctionMetadata(t *testing.T) {
	executor := expressionExecutor(t)
	for _, query := range []string{
		"SELECT REPLACE(CAST('a' AS BINARY), 'b', 'c')",
		"SELECT REVERSE(CAST('a' AS BINARY))",
		"SELECT LTRIM(CAST(' a' AS BINARY))",
		"SELECT RTRIM(CAST('a ' AS BINARY))",
		"SELECT TRIM(CAST(' a ' AS BINARY))",
		"SELECT UPPER(CAST('a' AS BINARY))",
		"SELECT LOWER(CAST('A' AS BINARY))",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
		metadata := result.metadata[0]
		if metadata.characterSet != mysqlCharsetBinary || metadata.flags&mysqlBinaryFlag == 0 {
			t.Errorf("execute(%q) metadata characterSet = %#x flags = %#x, want binary charset and flag", query, metadata.characterSet, metadata.flags)
		}
	}
}

func TestIssue376BinaryStringFunctionsPropagateNull(t *testing.T) {
	executor := expressionExecutor(t)
	for _, query := range []string{
		"SELECT REPLACE(CAST('a' AS BINARY), NULL, 'c')",
		"SELECT REVERSE(CAST(NULL AS BINARY))",
		"SELECT LTRIM(CAST(NULL AS BINARY))",
		"SELECT RTRIM(CAST(NULL AS BINARY))",
		"SELECT TRIM(CAST(NULL AS BINARY))",
		"SELECT UPPER(CAST(NULL AS BINARY))",
		"SELECT LOWER(CAST(NULL AS BINARY))",
		"SELECT CHAR_LENGTH(CAST(NULL AS BINARY))",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
		if len(result.rows) != 1 || len(result.nulls) != 1 || !result.nulls[0][0] {
			t.Errorf("execute(%q) rows = %#v nulls = %#v, want one NULL row", query, result.rows, result.nulls)
		}
	}
}
