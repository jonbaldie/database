package mysql

import "testing"

func TestStripSQLComments(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{query: "SELECT 1 -- comment", want: "SELECT 1  "},
		{query: "SELECT 1 --comment", want: "SELECT 1 --comment"},
		{query: "SELECT 1--1", want: "SELECT 1--1"},
		{query: "SELECT 1 # comment", want: "SELECT 1  "},
		{query: "SELECT /* c */ 1", want: "SELECT   1"},
		{query: "SELECT '/* not comment */'", want: "SELECT '/* not comment */'"},
		{query: "SELECT '-- not comment'", want: "SELECT '-- not comment'"},
		{query: "SELECT '# not comment'", want: "SELECT '# not comment'"},
		{query: "SELECT `id--x`", want: "SELECT `id--x`"},
		{query: "SELECT 1 /* unterminated", want: "SELECT 1 /* unterminated"},
		{query: "/* connector/j */ SELECT @@version", want: "  SELECT @@version"},
	}
	for _, test := range cases {
		if got := stripSQLComments(test.query); got != test.want {
			t.Errorf("stripSQLComments(%q) = %q, want %q", test.query, got, test.want)
		}
	}
}

func TestSQLCommentsDoNotChangeSupportedStatements(t *testing.T) {
	executor := ddlExecutorForTest(t)
	cases := []struct {
		query string
		want  [][]string
	}{
		{query: "/* comment */ SELECT 1", want: [][]string{{"1"}}},
		{query: "SELECT 1 -- comment\n", want: [][]string{{"1"}}},
		{query: "SELECT 1 -- comment", want: [][]string{{"1"}}},
		{query: "-- comment\nSELECT 1", want: [][]string{{"1"}}},
		{query: "SELECT 1 # comment", want: [][]string{{"1"}}},
		{query: "SELECT 1#comment", want: [][]string{{"1"}}},
		{query: "# comment\nSELECT 1", want: [][]string{{"1"}}},
		{query: "SELECT 1 /* comment */ + 1", want: [][]string{{"2"}}},
		{query: "SELECT /* c */ 1", want: [][]string{{"1"}}},
		{query: "SELECT 1 /* comment */", want: [][]string{{"1"}}},
		{query: "SELECT 1/*c*/+1", want: [][]string{{"2"}}},
		{query: "SELECT 1 /* line1\nline2 */ + 1", want: [][]string{{"2"}}},
		{query: "SELECT '-- not comment'", want: [][]string{{"-- not comment"}}},
		{query: "SELECT '# not comment'", want: [][]string{{"# not comment"}}},
		{query: "SELECT '/* not comment */'", want: [][]string{{"/* not comment */"}}},
		{query: "SELECT 'it''s -- not a comment'", want: [][]string{{"it's -- not a comment"}}},
		{query: "SELECT 1--1", want: [][]string{{"2"}}},
		{query: "SELECT 1; -- trailing", want: [][]string{{"1"}}},
	}
	for _, test := range cases {
		result, err := executeStatement(executor, test.query)
		if err != nil {
			t.Errorf("%q: %v", test.query, err)
			continue
		}
		if !equalRows(result.rows, test.want) {
			t.Errorf("%q rows = %#v, want %#v", test.query, result.rows, test.want)
		}
	}
}

func TestSQLCommentsInTableStatements(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items /* c */ (id INT PRIMARY KEY)",
		"INSERT INTO items VALUES (1) -- row",
		"INSERT INTO items VALUES (2) # row",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM items /* c */ ORDER BY id")
	if err != nil || !equalRows(result.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("rows = %#v, err = %v", result, err)
	}
}
