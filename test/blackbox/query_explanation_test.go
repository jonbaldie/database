package blackbox_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLPlannedExplanationUsesTheWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "explanation-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "explanation-secret")
	defer admin.close()
	for _, query := range []string{
		"CREATE DATABASE explanation",
		"USE explanation",
		"CREATE TABLE orders (id INT PRIMARY KEY, value INT)",
		"INSERT INTO orders VALUES (1, 10)",
	} {
		mustQuery(t, admin, query)
	}

	jsonResult := admin.query("EXPLAIN FORMAT=JSON SELECT id FROM orders WHERE id = 1")
	if jsonResult.err != "" || !slices.Equal(jsonResult.columns, []string{"EXPLAIN"}) || len(jsonResult.rows) != 1 || len(jsonResult.rows[0]) != 1 {
		t.Fatalf("JSON planned explanation: %#v", jsonResult)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(jsonResult.rows[0][0]), &document); err != nil {
		t.Fatalf("decode JSON planned explanation: %v", err)
	}
	if document["format_version"] != float64(1) || document["mode"] != "plan" || document["partial"] != false {
		t.Fatalf("JSON planned explanation envelope: %#v", document)
	}
	statement, ok := document["statement"].(map[string]any)
	if !ok || statement["kind"] != "select" || statement["current_database"] != "explanation" || statement["read_only"] != true || statement["locking_read"] != false {
		t.Fatalf("JSON planned explanation statement: %#v", document["statement"])
	}
	assertPublicPlan(t, document["plan"], false)
	plan := document["plan"].(map[string]any)
	if plan["kind"] != "project" {
		t.Fatalf("JSON planned explanation root: %#v", plan)
	}
	filter := plan["children"].([]any)[0].(map[string]any)
	if filter["kind"] != "filter" {
		t.Fatalf("JSON planned explanation filter: %#v", filter)
	}
	scan := filter["children"].([]any)[0].(map[string]any)
	strategy, ok := scan["strategy"].(map[string]any)
	if scan["kind"] != "scan" || !ok || strategy["name"] != "btree_covering_index_scan" {
		t.Fatalf("JSON planned explanation scan: %#v", scan)
	}

	tabular := admin.query("EXPLAIN SELECT id FROM orders WHERE id = 1")
	wantColumns := []string{
		"id", "select_type", "table", "partitions", "type", "possible_keys", "key", "key_len", "ref", "rows", "filtered", "Extra",
		"operator_id", "parent_operator_id", "operator", "strategy", "estimated_cost", "estimated_memory_bytes", "actual_rows", "loops", "first_row_ms", "total_ms", "actual_input_rows", "actual_filtered_rows", "actual_peak_memory_bytes", "actual_logical_reads", "actual_physical_reads", "actual_bytes_read", "actual_lock_ms", "actual_other_ms", "actual_rows_vs_estimate_ratio", "actual_warnings", "summary", "warnings",
	}
	if tabular.err != "" || !slices.Equal(tabular.columns, wantColumns) || len(tabular.rows) == 0 || len(tabular.nulls) != len(tabular.rows) {
		t.Fatalf("tabular planned explanation: %#v", tabular)
	}
	actualRows := slices.Index(tabular.columns, "actual_rows")
	if actualRows < 0 || !tabular.nulls[0][actualRows] {
		t.Fatalf("tabular plan runtime evidence must be NULL: %#v", tabular)
	}
}

func TestMySQLEqualityJoinUsesTheHashJoinStrategy(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "hash-join-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	client := newWireClient(t, address, "admin", "hash-join-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE joins",
		"USE joins",
		"CREATE TABLE authors (id INT PRIMARY KEY, name VARCHAR(32))",
		"CREATE TABLE posts (id INT PRIMARY KEY, author_id INT, title VARCHAR(32))",
		"INSERT INTO authors VALUES (1, 'Ada'), (2, 'Grace'), (3, 'Linus')",
		"INSERT INTO posts VALUES (10, 1, 'first'), (11, 1, 'second'), (12, 2, 'third')",
	} {
		mustQuery(t, client, query)
	}

	joined := client.query("SELECT authors.name, posts.title FROM authors JOIN posts ON authors.id = posts.author_id ORDER BY posts.id")
	if joined.err != "" || !slices.EqualFunc(joined.rows, [][]string{{"Ada", "first"}, {"Ada", "second"}, {"Grace", "third"}}, slices.Equal) {
		t.Fatalf("equality join: %#v", joined)
	}

	analyzed := client.query("EXPLAIN ANALYZE FORMAT=JSON SELECT authors.name, posts.title FROM authors JOIN posts ON authors.id = posts.author_id")
	if analyzed.err != "" || len(analyzed.rows) != 1 {
		t.Fatalf("analyze equality join: %#v", analyzed)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(analyzed.rows[0][0]), &document); err != nil {
		t.Fatalf("decode equality join analysis: %v", err)
	}
	join := findPublicPlanKind(t, document["plan"], "join")
	strategy, ok := join["strategy"].(map[string]any)
	if !ok || strategy["name"] != "hash_join" {
		t.Fatalf("equality join strategy: %#v", join)
	}
	actual := join["actual"].(map[string]any)
	if actual["input_rows"] != float64(6) {
		t.Fatalf("equality join input rows = %#v, want 6", actual["input_rows"])
	}
}

func findPublicPlanKind(t *testing.T, value any, kind string) map[string]any {
	t.Helper()
	plan, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("public plan node: %#v", value)
	}
	if plan["kind"] == kind {
		return plan
	}
	for _, child := range plan["children"].([]any) {
		if found := findPublicPlanKindOptional(child, kind); found != nil {
			return found
		}
	}
	t.Fatalf("public plan has no %s operator: %#v", kind, value)
	return nil
}

func findPublicPlanKindOptional(value any, kind string) map[string]any {
	plan, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if plan["kind"] == kind {
		return plan
	}
	children, _ := plan["children"].([]any)
	for _, child := range children {
		if found := findPublicPlanKindOptional(child, kind); found != nil {
			return found
		}
	}
	return nil
}

func assertPublicPlan(t *testing.T, value any, hasRuntimeEvidence bool) {
	t.Helper()
	plan, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("public plan node: %#v", value)
	}
	for _, field := range []string{"id", "kind", "summary", "operation", "estimates", "output", "warnings", "children"} {
		if _, found := plan[field]; !found {
			t.Errorf("public plan node %v lacks %q: %#v", plan["id"], field, plan)
		}
	}
	_, hasActual := plan["actual"]
	if hasActual != hasRuntimeEvidence {
		t.Errorf("public plan node %v runtime evidence: %#v", plan["id"], plan)
	}
	children, ok := plan["children"].([]any)
	if !ok {
		t.Errorf("public plan node %v children: %#v", plan["id"], plan)
		return
	}
	for _, child := range children {
		assertPublicPlan(t, child, hasRuntimeEvidence)
	}
}

func TestMySQLAnalyzeAndLiveExplanationUseTheWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "explanation-secret")
	process, address := startMySQLServer(t, runner, directory, "--lock-wait-timeout-ms=500")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "explanation-secret")
	defer admin.close()
	for _, query := range []string{
		"CREATE DATABASE explanation",
		"USE explanation",
		"CREATE TABLE orders (id INT PRIMARY KEY, value INT)",
		"INSERT INTO orders VALUES (1, 10)",
	} {
		mustQuery(t, admin, query)
	}

	analyzed := admin.query("EXPLAIN ANALYZE FORMAT=JSON SELECT id FROM orders WHERE id = 1")
	if analyzed.err != "" || len(analyzed.rows) != 1 {
		t.Fatalf("analyzed explanation: %#v", analyzed)
	}
	var analysis map[string]any
	if err := json.Unmarshal([]byte(analyzed.rows[0][0]), &analysis); err != nil {
		t.Fatalf("decode analyzed explanation: %v", err)
	}
	if analysis["mode"] != "analyze" || analysis["partial"] != false {
		t.Fatalf("analyzed envelope: %#v", analysis)
	}
	if complete, ok := analysis["timing"].(map[string]any)["execution"].(map[string]any)["complete"].(bool); !ok || !complete {
		t.Fatalf("analyzed timing: %#v", analysis["timing"])
	}
	if _, found := analysis["plan"].(map[string]any)["actual"]; !found {
		t.Fatalf("analyzed explanation has no runtime evidence: %#v", analysis)
	}
	analysisPlan := analysis["plan"].(map[string]any)
	actual := analysisPlan["actual"].(map[string]any)
	if actual["peak_memory_bytes"].(float64) <= 0 || actual["rows_vs_estimate_ratio"] == nil {
		t.Fatalf("analyzed root evidence: %#v", actual)
	}
	filter := analysisPlan["children"].([]any)[0].(map[string]any)
	scan := filter["children"].([]any)[0].(map[string]any)
	if scan["actual"].(map[string]any)["storage"].(map[string]any)["logical_reads"].(float64) <= 0 {
		t.Fatalf("analyzed scan reads: %#v", scan)
	}
	tabular := admin.query("EXPLAIN ANALYZE SELECT id FROM orders WHERE id = 1")
	if tabular.err != "" || len(tabular.rows) == 0 {
		t.Fatalf("tabular analyzed explanation: %#v", tabular)
	}
	actualRowsColumn := -1
	for index, column := range tabular.columns {
		if column == "actual_rows" {
			actualRowsColumn = index
			break
		}
	}
	if actualRowsColumn < 0 || tabular.rows[0][actualRowsColumn] == "" {
		t.Fatalf("tabular analyzed runtime evidence: %#v", tabular)
	}
	for _, required := range []string{"actual_peak_memory_bytes", "actual_logical_reads", "actual_lock_ms", "actual_rows_vs_estimate_ratio"} {
		found := false
		for _, column := range tabular.columns {
			found = found || column == required
		}
		if !found {
			t.Fatalf("tabular runtime column %q: %#v", required, tabular.columns)
		}
	}
	if result := admin.query("EXPLAIN ANALYZE UPDATE orders SET value = 20 WHERE id = 1"); result.errCode != 1235 {
		t.Fatalf("write analysis: %#v", result)
	}
	if result := admin.query("EXPLAIN ANALYZE SELECT missing FROM orders"); result.errCode != 1054 {
		t.Fatalf("failed analysis did not return the statement error: %#v", result)
	}
	if result := admin.query("EXPLAIN ANALYZE SELECT id FROM orders FOR UPDATE"); result.errCode != 1235 {
		t.Fatalf("locking analysis: %#v", result)
	}
	if result := admin.query("SELECT value FROM orders WHERE id = 1"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "10" {
		t.Fatalf("rejected write analysis changed data: %#v", result)
	}

	owner := newWireClient(t, address, "admin", "explanation-secret")
	defer owner.close()
	writer := newWireClient(t, address, "admin", "explanation-secret")
	defer writer.close()
	observer := newWireClient(t, address, "admin", "explanation-secret")
	defer observer.close()
	for _, client := range []*wireClient{owner, writer, observer} {
		mustQuery(t, client, "USE explanation")
	}
	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM orders WHERE id = 1 FOR UPDATE")
	mustQuery(t, writer, "BEGIN")
	blocked := queryAsync(writer, "UPDATE orders SET value = 20 WHERE id = 1")
	mustRemainBlocked(t, blocked)

	snapshot := liveQueryExplanation(t, observer, writer.connectionID)
	if snapshot["mode"] != "snapshot" || snapshot["partial"] != true {
		t.Fatalf("snapshot envelope: %#v", snapshot)
	}
	capture := snapshot["snapshot"].(map[string]any)
	if capture["connection_id"] != float64(writer.connectionID) || capture["captured_at"] == "" {
		t.Fatalf("snapshot capture identity: %#v", capture)
	}
	if _, found := snapshot["plan"].(map[string]any)["actual"]; !found {
		t.Fatalf("snapshot has no partial evidence: %#v", snapshot)
	}
	if snapshot["plan"].(map[string]any)["actual"].(map[string]any)["wait"].(map[string]any)["lock_ms"].(float64) <= 0 {
		t.Fatalf("snapshot does not report the observed lock wait: %#v", snapshot)
	}
	mustRemainBlocked(t, blocked)
	mustQuery(t, owner, "COMMIT")
	mustQueryResult(t, <-blocked, "blocked write after live explanation")
	mustQuery(t, writer, "COMMIT")

	preparedWriter := newWireClient(t, address, "admin", "explanation-secret")
	defer preparedWriter.close()
	mustQuery(t, preparedWriter, "USE explanation")
	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM orders WHERE id = 1 FOR UPDATE")
	mustQuery(t, preparedWriter, "BEGIN")
	prepared := preparedWriter.prepare("SELECT id FROM orders WHERE id = ? FOR UPDATE")
	if prepared.err != "" {
		t.Fatalf("prepare live statement: %#v", prepared)
	}
	defer preparedWriter.closePrepared(prepared.id)
	preparedBlocked := executePreparedAsync(preparedWriter, prepared.id, []preparedParameter{{typ: 0x03, value: []byte{1, 0, 0, 0}}})
	mustRemainBlocked(t, preparedBlocked)
	preparedDocument := liveQueryExplanation(t, observer, preparedWriter.connectionID)
	preparedStatement := preparedDocument["statement"].(map[string]any)
	if preparedStatement["sql"] != "SELECT id FROM orders WHERE id = ? FOR UPDATE" {
		t.Fatalf("prepared explanation SQL: %#v", preparedStatement)
	}
	parameters := preparedStatement["parameters"].([]any)
	if len(parameters) != 1 || parameters[0].(map[string]any)["position"] != float64(1) || parameters[0].(map[string]any)["type"] != "INT" {
		t.Fatalf("prepared explanation parameters: %#v", parameters)
	}
	mustRemainBlocked(t, preparedBlocked)
	mustQuery(t, owner, "COMMIT")
	mustQueryResult(t, <-preparedBlocked, "blocked prepared read after live explanation")
	mustQuery(t, preparedWriter, "COMMIT")

	if result := observer.query("EXPLAIN FOR CONNECTION 999999"); result.errCode != 1094 {
		t.Fatalf("missing live connection: %#v", result)
	}
}

func liveQueryExplanation(t *testing.T, client *wireClient, connectionID uint32) map[string]any {
	t.Helper()
	result := client.query("EXPLAIN FORMAT=JSON FOR CONNECTION " + strconv.FormatUint(uint64(connectionID), 10))
	if result.err != "" || len(result.rows) != 1 {
		t.Fatalf("live Query explanation: %#v", result)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
		t.Fatalf("decode live Query explanation: %v", err)
	}
	return document
}

func executePreparedAsync(client *wireClient, id uint32, parameters []preparedParameter) <-chan wireResult {
	completed := make(chan wireResult, 1)
	go func() { completed <- client.executePreparedValues(id, parameters) }()
	return completed
}
