package mysql

import "testing"

// Issue #362: LIKE applied byte-by-byte pattern matching to binary values.
// A BINARY column value and a CAST AS BINARY result carried no binary collation
// tag, so the matcher folded case and matched case variants that differ at the
// byte level.
func TestIssue362BinaryLikeIsByteExact(t *testing.T) {
	executor := expressionExecutor(t)
	cases := []struct {
		query string
		want  string
	}{
		{"SELECT CAST('a' AS BINARY) LIKE 'A'", "0"},
		{"SELECT CAST('a' AS BINARY) LIKE CAST('A' AS BINARY)", "0"},
		{"SELECT 'a' LIKE CAST('A' AS BINARY)", "0"},
		{"SELECT CAST('a' AS BINARY) LIKE 'a'", "1"},
		{"SELECT CAST('a' AS BINARY) LIKE CAST('a' AS BINARY)", "1"},
		{"SELECT CAST('abc' AS BINARY) LIKE 'a%'", "1"},
		{"SELECT CAST('Abc' AS BINARY) LIKE 'a%'", "0"},
		{"SELECT CAST('abc' AS BINARY) LIKE '_bc'", "1"},
		{"SELECT CAST('abc' AS BINARY) NOT LIKE 'A%'", "1"},
		// Ordinary text keeps its collation-driven folding.
		{"SELECT 'a' LIKE 'A'", "1"},
		{"SELECT 'a' LIKE CAST('a' AS BINARY)", "1"},
	}
	for _, tc := range cases {
		result, err := executeStatement(executor, tc.query)
		if err != nil {
			t.Fatalf("execute(%q): %v", tc.query, err)
		}
		if !equalRows(result.rows, [][]string{{tc.want}}) {
			t.Errorf("execute(%q) rows = %#v, want [[%s]]", tc.query, result.rows, tc.want)
		}
	}
}

func TestIssue362BinaryColumnLikeIsByteExact(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE binvals (v BINARY(1))",
		"INSERT INTO binvals VALUES ('a')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}

	mismatch, err := executeStatement(executor, "SELECT v FROM binvals WHERE v LIKE 'A'")
	if err != nil {
		t.Fatalf("LIKE A: %v", err)
	}
	if len(mismatch.rows) != 0 {
		t.Fatalf("BINARY(1) 'a' LIKE 'A' = %v, want no rows", mismatch.rows)
	}

	match, err := executeStatement(executor, "SELECT v FROM binvals WHERE v LIKE 'a'")
	if err != nil {
		t.Fatalf("LIKE a: %v", err)
	}
	if !equalRows(match.rows, [][]string{{"a"}}) {
		t.Fatalf("BINARY(1) 'a' LIKE 'a' = %v, want [[a]]", match.rows)
	}
}
