package mysql

import "testing"

// Issue #373: the expression tokenizer only recognized single-quoted string
// literals, so a double-quoted literal failed with 1064 in projections,
// predicates, and INSERT ... VALUES. In default SQL mode MySQL treats both
// quote characters as string delimiters.
func TestIssue373DoubleQuotedStringProjections(t *testing.T) {
	executor := expressionExecutor(t)
	cases := []struct {
		query string
		want  []string
	}{
		{`SELECT "abc"`, []string{"abc"}},
		{`SELECT "abc", "def"`, []string{"abc", "def"}},
		{`SELECT "it\"s", "it""s"`, []string{`it"s`, `it"s`}},
		{`SELECT "it's", 'it"s'`, []string{"it's", `it"s`}},
		{`SELECT CONCAT("a", 'b')`, []string{"ab"}},
		{`SELECT "abc" = 'abc'`, []string{"1"}},
		{`SELECT 'it''s'`, []string{"it's"}},
	}
	for _, tc := range cases {
		result, err := executeStatement(executor, tc.query)
		if err != nil {
			t.Fatalf("execute(%q): %v", tc.query, err)
		}
		if !equalRows(result.rows, [][]string{tc.want}) {
			t.Errorf("execute(%q) rows = %#v, want %#v", tc.query, result.rows, [][]string{tc.want})
		}
	}

	result, err := executeStatement(executor, `SELECT "abc"`)
	if err != nil {
		t.Fatalf(`SELECT "abc": %v`, err)
	}
	if metadata := result.metadata[0]; metadata.typ != mysqlTypeVarString {
		t.Fatalf(`SELECT "abc" metadata = %#v, want VARCHAR wire type`, metadata)
	}
}

func TestIssue373UnterminatedDoubleQuotedStringIsSyntaxError(t *testing.T) {
	executor := expressionExecutor(t)
	for _, query := range []string{`SELECT "abc`, `SELECT "abc\"`} {
		if _, err := executeStatement(executor, query); !isFailureCode(err, 1064) {
			t.Errorf("execute(%q) = %v, want 1064", query, err)
		}
	}
}

func TestIssue373DoubleQuotedStringInsertAndPredicate(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE quoted_vals (v VARCHAR(10), n INT)",
		`INSERT INTO quoted_vals VALUES ("abc", 1)`,
		`INSERT INTO quoted_vals VALUES ('def', 2)`,
		`INSERT INTO quoted_vals (v, n) VALUES ("it""s", 3)`,
		`INSERT INTO quoted_vals VALUES ("old", 4)`,
		`UPDATE quoted_vals SET v = "new" WHERE v = "old"`,
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}
	cases := []struct {
		query string
		want  [][]string
	}{
		{`SELECT v, n FROM quoted_vals WHERE v = "abc"`, [][]string{{"abc", "1"}}},
		{`SELECT n FROM quoted_vals WHERE v = "it\"s"`, [][]string{{"3"}}},
		{`SELECT v FROM quoted_vals WHERE n = 2`, [][]string{{"def"}}},
		{`SELECT v FROM quoted_vals GROUP BY v HAVING v = "def"`, [][]string{{"def"}}},
		{"SELECT `v` FROM quoted_vals WHERE n = 1", [][]string{{"abc"}}},
		{`SELECT n FROM quoted_vals WHERE v = "new"`, [][]string{{"4"}}},
	}
	for _, tc := range cases {
		result, err := executeStatement(executor, tc.query)
		if err != nil {
			t.Fatalf("execute(%q): %v", tc.query, err)
		}
		if !equalRows(result.rows, tc.want) {
			t.Errorf("execute(%q) rows = %#v, want %#v", tc.query, result.rows, tc.want)
		}
	}
}
