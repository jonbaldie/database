package mysql

import (
	"encoding/json"
	"testing"
)

func issue465Executor(t *testing.T) *textStatementExecutor {
	t.Helper()
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, v VARCHAR(10))",
		"INSERT INTO t VALUES (1, 'a'), (2, 'b'), (3, 'c')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	return executor
}

func issue465Operators(t *testing.T, encoded string) []map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(encoded), &document); err != nil {
		t.Fatalf("decode explanation: %v", err)
	}
	operators := []map[string]any{}
	var visit func(map[string]any)
	visit = func(operator map[string]any) {
		operators = append(operators, operator)
		for _, child := range operator["children"].([]any) {
			visit(child.(map[string]any))
		}
	}
	visit(document["plan"].(map[string]any))
	return operators
}

func issue465Kind(operators []map[string]any, kind string) map[string]any {
	for _, operator := range operators {
		if operator["kind"] == kind {
			return operator
		}
	}
	return nil
}

func TestExplainAnalyzeRecordsThePointLookupThatQueriesRun(t *testing.T) {
	executor := issue465Executor(t)
	result, err := executeStatement(executor, "EXPLAIN ANALYZE FORMAT=JSON SELECT v FROM t WHERE id = 3")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	operators := issue465Operators(t, result.rows[0][0])
	if scan := issue465Kind(operators, "scan"); scan != nil {
		t.Fatalf("analysis reports a scan the point lookup never runs: %#v", scan)
	}
	lookup := issue465Kind(operators, "lookup")
	if lookup == nil {
		t.Fatalf("analysis has no lookup operator: %s", result.rows[0][0])
	}
	operation := lookup["operation"].(map[string]any)
	if operation["lookup_type"] != "point" || operation["unique"] != true {
		t.Fatalf("lookup operation = %#v", operation)
	}
	actual, ok := lookup["actual"].(map[string]any)
	if !ok || actual["invocations"] != float64(1) || actual["output_rows"] != float64(1) {
		t.Fatalf("lookup actual = %#v", lookup["actual"])
	}
	if session := executor.session; session.resources != nil {
		t.Fatalf("analysis left statement resources on the session")
	}
}

func TestExplainPlanReportsThePointLookup(t *testing.T) {
	executor := issue465Executor(t)
	result, err := executeStatement(executor, "EXPLAIN FORMAT=JSON SELECT v FROM t WHERE id = 3")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	operators := issue465Operators(t, result.rows[0][0])
	if issue465Kind(operators, "lookup") == nil || issue465Kind(operators, "scan") != nil {
		t.Fatalf("plan does not report the point lookup: %s", result.rows[0][0])
	}
}

func TestExplainAnalyzeRecordsLookupMissVerification(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE s (k VARCHAR(10) PRIMARY KEY, v VARCHAR(10))",
		"INSERT INTO s VALUES ('abc', 'x'), ('def', 'y')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	// 'ABC' misses the exact key probe, so the lookup verifies the miss by
	// comparing the stored rows. The row still matches under the collation.
	result, err := executeStatement(executor, "EXPLAIN ANALYZE FORMAT=JSON SELECT v FROM s WHERE k = 'ABC'")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	operators := issue465Operators(t, result.rows[0][0])
	if scan := issue465Kind(operators, "scan"); scan != nil {
		t.Fatalf("analysis reports a scan the point lookup never runs: %#v", scan)
	}
	lookup := issue465Kind(operators, "lookup")
	if lookup == nil {
		t.Fatalf("analysis has no lookup operator: %s", result.rows[0][0])
	}
	actual := lookup["actual"].(map[string]any)
	if actual["invocations"] != float64(1) || actual["output_rows"].(float64) < 1 {
		t.Fatalf("lookup actual = %#v", actual)
	}
	root := operators[0]["actual"].(map[string]any)
	if root["output_rows"] != float64(1) {
		t.Fatalf("root actual = %#v", root)
	}
	plain, err := executeStatement(executor, "SELECT v FROM s WHERE k = 'ABC'")
	if err != nil || len(plain.rows) != 1 || plain.rows[0][0] != "x" {
		t.Fatalf("plain query = %v, %v", plain, err)
	}
}

func TestLiveExplanationRecordsOperatorCounters(t *testing.T) {
	executor := issue465Executor(t)
	session := executor.session
	session.connectionID = 465
	plan, err := executor.planExplanation("SELECT v FROM t WHERE id = 3")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	recorder, finish := session.server.explanations.begin(session.connectionID, plan, session)
	executor.recorder = recorder
	result, err := executeStatement(executor, "SELECT v FROM t WHERE id = 3")
	if err != nil || len(result.rows) != 1 || result.rows[0][0] != "c" {
		t.Fatalf("select = %v, %v", result, err)
	}
	document, ok := session.server.explanations.snapshot(session.connectionID)
	finish()
	if !ok {
		t.Fatal("no live explanation")
	}
	encoded, err := renderExplanation("json", document)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	lookup := issue465Kind(issue465Operators(t, encoded.rows[0][0]), "lookup")
	if lookup == nil {
		t.Fatalf("snapshot has no lookup operator: %s", encoded.rows[0][0])
	}
	actual, ok := lookup["actual"].(map[string]any)
	if !ok || actual["output_rows"] != float64(1) {
		t.Fatalf("snapshot lookup actual = %#v", lookup["actual"])
	}
}
