package mysql

import "testing"

func TestIssue409ReplaceHonoursCollation(t *testing.T) {
	executor := expressionExecutor(t)
	cases := map[string]string{
		"SELECT REPLACE('HELLO', 'l', 'x')":                                     "HExxO",
		"SELECT REPLACE('HELLO', 'L', 'x')":                                     "HExxO",
		"SELECT REPLACE('aB', 'b', 'c')":                                        "ac",
		"SELECT REPLACE('café', 'E', 'x')":                                      "cafx",
		"SELECT REPLACE('abab', 'AB', '')":                                      "",
		"SELECT REPLACE('aaa', 'aa', 'b')":                                      "ba",
		"SELECT REPLACE('🔥a🔥', 'A', 'b')":                                       "🔥b🔥",
		"SELECT REPLACE(CAST('aB' AS BINARY), 'b', 'c') = CAST('aB' AS BINARY)": "1",
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
