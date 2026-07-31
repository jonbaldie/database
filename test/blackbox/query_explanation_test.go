package blackbox_test

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

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

	snapshotResult := observer.query("EXPLAIN FORMAT=JSON FOR CONNECTION " + strconv.FormatUint(uint64(writer.connectionID), 10))
	if snapshotResult.err != "" || len(snapshotResult.rows) != 1 {
		t.Fatalf("live explanation: %#v", snapshotResult)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(snapshotResult.rows[0][0]), &snapshot); err != nil {
		t.Fatalf("decode live explanation: %v", err)
	}
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
	preparedSnapshot := observer.query("EXPLAIN FORMAT=JSON FOR CONNECTION " + strconv.FormatUint(uint64(preparedWriter.connectionID), 10))
	if preparedSnapshot.err != "" || len(preparedSnapshot.rows) != 1 {
		t.Fatalf("prepared live explanation: %#v", preparedSnapshot)
	}
	var preparedDocument map[string]any
	if err := json.Unmarshal([]byte(preparedSnapshot.rows[0][0]), &preparedDocument); err != nil {
		t.Fatalf("decode prepared live explanation: %v", err)
	}
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

func executePreparedAsync(client *wireClient, id uint32, parameters []preparedParameter) <-chan wireResult {
	completed := make(chan wireResult, 1)
	go func() { completed <- client.executePreparedValues(id, parameters) }()
	return completed
}
