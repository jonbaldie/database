package mysql

import "testing"

func TestIssue361BinaryCastComparisonsUseByteSemantics(t *testing.T) {
	executor := expressionExecutor(t)
	cases := []struct {
		query string
		want  string
	}{
		{"SELECT CAST('a' AS BINARY) = CAST('A' AS BINARY)", "0"},
		{"SELECT CAST('a' AS BINARY) <=> CAST('A' AS BINARY)", "0"},
		{"SELECT CAST('a' AS BINARY) < CAST('B' AS BINARY)", "0"},
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

	result, err := executeStatement(executor, "SELECT 'a' = 'A'")
	if err != nil {
		t.Fatalf("ordinary text comparison: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"1"}}) {
		t.Fatalf("ordinary text comparison rows = %#v, want [[1]]", result.rows)
	}
}

func TestIssue361BinaryCastDistinctKeepsCaseVariants(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE vals (v VARCHAR(10))",
		"INSERT INTO vals VALUES ('a'), ('A')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}

	binaryResult, err := executeStatement(executor, "SELECT DISTINCT CAST(v AS BINARY) AS b FROM vals ORDER BY b")
	if err != nil {
		t.Fatalf("binary DISTINCT: %v", err)
	}
	if !equalRows(binaryResult.rows, [][]string{{"A"}, {"a"}}) {
		t.Fatalf("binary DISTINCT rows = %#v, want [[A] [a]]", binaryResult.rows)
	}

	countResult, err := executeStatement(executor, "SELECT COUNT(DISTINCT CAST(v AS BINARY)) FROM vals")
	if err != nil {
		t.Fatalf("binary COUNT DISTINCT: %v", err)
	}
	if !equalRows(countResult.rows, [][]string{{"2"}}) {
		t.Fatalf("binary COUNT DISTINCT rows = %#v, want [[2]]", countResult.rows)
	}

	textResult, err := executeStatement(executor, "SELECT DISTINCT v FROM vals ORDER BY v")
	if err != nil {
		t.Fatalf("ordinary text DISTINCT: %v", err)
	}
	if !equalRows(textResult.rows, [][]string{{"a"}}) {
		t.Fatalf("ordinary text DISTINCT rows = %#v, want [[a]]", textResult.rows)
	}
}

func TestIssue361BinaryCastGroupByKeepsCaseVariants(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE group_vals (v VARCHAR(10))",
		"INSERT INTO group_vals VALUES ('a'), ('A')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT CAST(v AS BINARY) AS b, COUNT(*) AS n FROM group_vals GROUP BY b ORDER BY b")
	if err != nil {
		t.Fatalf("binary GROUP BY: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"A", "1"}, {"a", "1"}}) {
		t.Fatalf("binary GROUP BY rows = %#v, want [[A 1] [a 1]]", result.rows)
	}
}

func TestIssue361BinaryCastJoinComparisonIsBytewise(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, query := range []string{
		"CREATE TABLE left_vals (v VARCHAR(10))",
		"CREATE TABLE right_vals (v VARCHAR(10))",
		"INSERT INTO left_vals VALUES ('a'), ('A')",
		"INSERT INTO right_vals VALUES ('A'), ('a')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT l.v, r.v FROM left_vals AS l JOIN right_vals AS r ON CAST(l.v AS BINARY) = CAST(r.v AS BINARY) ORDER BY CAST(l.v AS BINARY), CAST(r.v AS BINARY)")
	if err != nil {
		t.Fatalf("binary join: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"A", "A"}, {"a", "a"}}) {
		t.Fatalf("binary join rows = %#v, want [[A A] [a a]]", result.rows)
	}
}

func TestIssue361BinaryCastSetAndSubqueryComparisonsAreBytewise(t *testing.T) {
	executor := relationalSelectExecutor(t)

	setResult, err := executeStatement(executor, "SELECT CAST('a' AS BINARY) AS b UNION SELECT CAST('A' AS BINARY) ORDER BY b")
	if err != nil {
		t.Fatalf("binary UNION DISTINCT: %v", err)
	}
	if !equalRows(setResult.rows, [][]string{{"A"}, {"a"}}) {
		t.Fatalf("binary UNION DISTINCT rows = %#v, want [[A] [a]]", setResult.rows)
	}

	for _, query := range []string{
		"CREATE TABLE in_left (v VARCHAR(10))",
		"CREATE TABLE in_right (v VARCHAR(10))",
		"INSERT INTO in_left VALUES ('a'), ('A')",
		"INSERT INTO in_right VALUES ('A')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute(%q): %v", query, err)
		}
	}

	inResult, err := executeStatement(executor, "SELECT l.v FROM in_left AS l WHERE CAST(l.v AS BINARY) IN (SELECT CAST(r.v AS BINARY) FROM in_right AS r) ORDER BY CAST(l.v AS BINARY)")
	if err != nil {
		t.Fatalf("binary IN subquery: %v", err)
	}
	if !equalRows(inResult.rows, [][]string{{"A"}}) {
		t.Fatalf("binary IN subquery rows = %#v, want [[A]]", inResult.rows)
	}
}
