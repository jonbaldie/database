package mysql

import "testing"

func issue408ExecutorForTest(t *testing.T) *textStatementExecutor {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(20))",
		"INSERT INTO t VALUES (1, 'count(*)'), (2, 'other'), (3, '1')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	return executor
}

func TestIssue408QuotedAggregateNameProjectsLiteralValue(t *testing.T) {
	executor := issue408ExecutorForTest(t)
	result, err := executeStatement(executor, "SELECT 'count(*)' FROM t")
	if err != nil || !equalRows(result.rows, [][]string{{"count(*)"}, {"count(*)"}, {"count(*)"}}) {
		t.Fatalf("SELECT 'count(*)' FROM t = %#v, err = %v", result, err)
	}
}

func TestIssue408QuotedAggregateNameWithUnknownColumnProjectsLiteralValue(t *testing.T) {
	executor := issue408ExecutorForTest(t)
	result, err := executeStatement(executor, "SELECT 'count(nonexistent)' FROM t")
	if err != nil || !equalRows(result.rows, [][]string{{"count(nonexistent)"}, {"count(nonexistent)"}, {"count(nonexistent)"}}) {
		t.Fatalf("SELECT 'count(nonexistent)' FROM t = %#v, err = %v", result, err)
	}
}

func TestIssue408HavingLeavesQuotedAggregateNameUnevaluated(t *testing.T) {
	executor := issue408ExecutorForTest(t)
	result, err := executeStatement(executor, "SELECT name FROM t GROUP BY name HAVING name = 'count(*)'")
	if err != nil || !equalRows(result.rows, [][]string{{"count(*)"}}) {
		t.Fatalf("HAVING name = 'count(*)' = %#v, err = %v", result, err)
	}
}

func TestIssue408OrderByLeavesQuotedAggregateNameUnevaluated(t *testing.T) {
	executor := issue408ExecutorForTest(t)
	result, err := executeStatement(executor, "SELECT name FROM t GROUP BY name ORDER BY (name = 'count(nonexistent)')")
	if err != nil || len(result.rows) != 3 {
		t.Fatalf("ORDER BY (name = 'count(nonexistent)') = %#v, err = %v", result, err)
	}
}

func TestIssue408QuotedWindowPatternProjectsLiteralValue(t *testing.T) {
	executor := issue408ExecutorForTest(t)
	result, err := executeStatement(executor, "SELECT 'ROW_NUMBER() OVER ()' FROM t")
	want := [][]string{{"ROW_NUMBER() OVER ()"}, {"ROW_NUMBER() OVER ()"}, {"ROW_NUMBER() OVER ()"}}
	if err != nil || !equalRows(result.rows, want) {
		t.Fatalf("SELECT 'ROW_NUMBER() OVER ()' FROM t = %#v, err = %v", result, err)
	}
}
