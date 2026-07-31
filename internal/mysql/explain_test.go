package mysql

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func explainExecutor(t *testing.T) *textStatementExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	columns := []string{"id", "customer_id", "total"}
	types := []string{"BIGINT", "BIGINT", "INT"}
	if err := store.CreateTableWithTypes("app", "orders", columns, types); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := store.Insert("app", "orders", []string{"1", "7", "50"}); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	session := &session{server: server, database: "app", initialDB: "app", timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{}}
	return &textStatementExecutor{session: session}
}

func TestExplainTraditionalProjection(t *testing.T) {
	executor := explainExecutor(t)
	result, err := executor.execute("EXPLAIN SELECT id FROM orders WHERE customer_id = '7'")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	for _, required := range []string{"operator_id", "parent_operator_id", "operator", "strategy", "estimated_cost", "actual_rows", "summary", "warnings"} {
		if !slices.Contains(result.columns, required) {
			t.Errorf("traditional projection missing %q", required)
		}
	}
	operator := slices.Index(result.columns, "operator")
	kinds := make([]string, len(result.rows))
	for index, row := range result.rows {
		kinds[index] = row[operator]
	}
	if !slices.Equal(kinds, []string{"project", "filter", "scan"}) {
		t.Fatalf("operator column = %q", kinds)
	}
	actual := slices.Index(result.columns, "actual_rows")
	if !result.nulls[0][actual] {
		t.Error("plan-only actual_rows is not SQL NULL")
	}
}

func TestExplainJSONDocument(t *testing.T) {
	executor := explainExecutor(t)
	result, err := executor.execute("EXPLAIN FORMAT=JSON SELECT * FROM orders")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !slices.Equal(result.columns, []string{"EXPLAIN"}) || len(result.rows) != 1 {
		t.Fatalf("unexpected json result shape: %#v", result.columns)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
		t.Fatalf("decode explanation: %v", err)
	}
	if document["format_version"] != float64(1) || document["mode"] != "plan" {
		t.Fatalf("unexpected envelope: %#v", document)
	}
	if document["plan"].(map[string]any)["kind"] != "scan" {
		t.Fatalf("all-column select root kind = %v", document["plan"].(map[string]any)["kind"])
	}
}

func TestExplainWriteJSON(t *testing.T) {
	executor := explainExecutor(t)
	result, err := executor.execute("EXPLAIN FORMAT=JSON INSERT INTO orders (id, customer_id, total) VALUES ('2','9','25')")
	if err != nil {
		t.Fatalf("explain insert: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
		t.Fatalf("decode explanation: %v", err)
	}
	if document["statement"].(map[string]any)["read_only"] != false {
		t.Error("insert reported read_only")
	}
	plan := document["plan"].(map[string]any)
	if plan["kind"] != "mutation" || plan["operation"].(map[string]any)["mutation_type"] != "insert" {
		t.Fatalf("unexpected write plan: %#v", plan)
	}
}

func TestExplainRejectsUnsupportedModes(t *testing.T) {
	executor := explainExecutor(t)
	if _, err := executor.execute("EXPLAIN SELECT * FROM missing_table"); err == nil {
		t.Error("EXPLAIN of unknown table was accepted")
	}
}

func TestExplainAnalyzeReportsCompletedRuntimeEvidence(t *testing.T) {
	executor := explainExecutor(t)
	result, err := executor.execute("EXPLAIN ANALYZE FORMAT=JSON SELECT * FROM orders")
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
		t.Fatalf("decode analysis: %v", err)
	}
	if document["mode"] != "analyze" || document["partial"] != false {
		t.Fatalf("analysis envelope = %#v", document)
	}
	if complete, ok := document["timing"].(map[string]any)["execution"].(map[string]any)["complete"].(bool); !ok || !complete {
		t.Fatalf("analysis timing = %#v", document["timing"])
	}
	actual := document["plan"].(map[string]any)["actual"].(map[string]any)
	if actual["output_rows"] != float64(1) || actual["total_ms"] == nil {
		t.Fatalf("analysis runtime evidence = %#v", actual)
	}
}

func TestExplainAnalyzeAssignsEvidenceToEveryExecutedRelationalOperator(t *testing.T) {
	executor := explainExecutor(t)
	for _, statement := range []string{
		"CREATE TABLE customers (id BIGINT, name VARCHAR(20))",
		"INSERT INTO customers (id, name) VALUES (7, 'Ada')",
		"INSERT INTO orders (id, customer_id, total) VALUES (2, 7, 20)",
	} {
		if _, err := executor.execute(statement); err != nil {
			t.Fatalf("seed %q: %v", statement, err)
		}
	}
	queries := map[string]string{
		"join":      "SELECT orders.id, customers.name FROM orders JOIN customers ON orders.customer_id = customers.id",
		"aggregate": "SELECT customer_id, COUNT(*) AS order_count FROM orders GROUP BY customer_id HAVING COUNT(*) > 0",
		"window":    "SELECT id, ROW_NUMBER() OVER (ORDER BY id) AS ordinal FROM orders",
		"cte":       "WITH order_ids AS (SELECT id FROM orders) SELECT id FROM order_ids UNION SELECT id FROM orders",
		"derived":   "SELECT id FROM (SELECT id FROM orders) AS nested UNION SELECT id FROM orders",
		"subquery":  "SELECT id, (SELECT id FROM orders LIMIT 1) AS other_id FROM orders UNION SELECT id, id FROM orders",
		"set":       "SELECT id FROM orders UNION SELECT id FROM orders UNION SELECT id FROM orders ORDER BY id DESC LIMIT 2",
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			result, err := executor.execute("EXPLAIN ANALYZE FORMAT=JSON " + query)
			if err != nil {
				t.Fatalf("analyze: %v", err)
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
				t.Fatalf("decode analysis: %v", err)
			}
			assertCompletedOperatorEvidence(t, document["plan"].(map[string]any))
		})
	}
}

func assertCompletedOperatorEvidence(t *testing.T, operator map[string]any) {
	t.Helper()
	actual, found := operator["actual"].(map[string]any)
	if !found {
		t.Fatalf("%s operator has no actual evidence: %#v", operator["kind"], operator)
	}
	if warnings, found := actual["warnings"].([]any); found {
		for _, warning := range warnings {
			if warning.(map[string]any)["code"] == "RUNTIME_OPERATOR_NOT_INVOKED" {
				t.Fatalf("%s operator lacks observed evidence: %#v", operator["kind"], actual)
			}
		}
	}
	for _, child := range operator["children"].([]any) {
		assertCompletedOperatorEvidence(t, child.(map[string]any))
	}
}

func TestExplainAnalyzeRejectsWritesAndLockingReads(t *testing.T) {
	executor := explainExecutor(t)
	for _, query := range []string{
		"EXPLAIN ANALYZE INSERT INTO orders (id, customer_id, total) VALUES ('2', '8', '20')",
		"EXPLAIN ANALYZE SELECT * FROM orders FOR UPDATE",
	} {
		if _, err := executor.execute(query); !isFailureCode(err, 1235) {
			t.Errorf("%q error = %v, want unsupported analysis error", query, err)
		}
	}
	result, err := executor.execute("SELECT * FROM orders")
	if err != nil || len(result.rows) != 1 {
		t.Fatalf("rejected analysis changed rows: result=%#v err=%v", result, err)
	}
}

func TestExplainScalarSubstringFromSyntax(t *testing.T) {
	executor := explainExecutor(t)
	result, err := executor.execute("EXPLAIN SELECT SUBSTRING('abcdef' FROM 2 FOR 3)")
	if err != nil {
		t.Fatalf("explain substring: %v", err)
	}
	if len(result.rows) == 0 || len(result.rows[0]) == 0 {
		t.Fatalf("explain substring returned no plan: %#v", result)
	}
}

// TestExplainMatchesExecutorAcceptance guards that EXPLAIN never describes a
// plan for a statement the executor itself would reject.
func TestExplainMatchesExecutorAcceptance(t *testing.T) {
	rejected := []string{
		"EXPLAIN SELECT id FROM orders WHERE missing_col = '1'",
		"EXPLAIN SELECT id FROM orders WHERE total > '1'",
		"EXPLAIN SELECT missing_col FROM orders",
		"EXPLAIN UPDATE orders SET missing_col = '1' WHERE id = '1'",
		"EXPLAIN INSERT INTO orders (id, missing_col) VALUES ('1','2')",
		"EXPLAIN DELETE FROM orders WHERE missing_col = '1'",
	}
	for _, statement := range rejected {
		executor := explainExecutor(t)
		explainResult, explainErr := executor.execute(statement)
		runResult, runErr := executor.execute(statement[len("EXPLAIN "):])
		if explainErr == nil {
			t.Errorf("%q produced a plan but the executor result was %v/%v", statement, runResult, runErr)
		}
		_ = explainResult
	}
}
