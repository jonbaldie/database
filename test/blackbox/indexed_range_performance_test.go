package blackbox_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIndexedRangePreservesRowsAndAnalyzeStorageEvidence(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE tasks (id INT PRIMARY KEY, created INT, title VARCHAR(64), INDEX idx_created (created))",
		"INSERT INTO tasks VALUES (1, 1, 'a'), (2, 2, 'bb'), (3, 3, 'ccc')",
	} {
		mustQuery(t, client, query)
	}
	query := "SELECT id, title FROM tasks FORCE INDEX (idx_created) WHERE created >= 2 AND created < 4"
	result := client.query(query)
	if result.err != "" || !slices.EqualFunc(result.rows, [][]string{{"2", "bb"}, {"3", "ccc"}}, slices.Equal) {
		t.Fatalf("indexed range rows = %#v", result)
	}
	analyzed := client.query("EXPLAIN ANALYZE FORMAT=JSON " + query)
	if analyzed.err != "" || len(analyzed.rows) != 1 {
		t.Fatalf("analyzed range = %#v", analyzed)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(analyzed.rows[0][0]), &document); err != nil {
		t.Fatal(err)
	}
	scan := findPublicPlanKind(t, document["plan"], "scan")
	if strategy := scan["strategy"].(map[string]any); strategy["name"] != "btree_index_scan" {
		t.Fatalf("range strategy = %#v", strategy)
	}
	actual := scan["actual"].(map[string]any)
	storage := actual["storage"].(map[string]any)
	if actual["output_rows"] != float64(2) || storage["logical_reads"] != float64(2) || storage["bytes_read"] != float64(12) {
		t.Fatalf("range scan evidence = %#v", actual)
	}
	result = client.query(query)
	if result.err != "" || !slices.EqualFunc(result.rows, [][]string{{"2", "bb"}, {"3", "ccc"}}, slices.Equal) {
		t.Fatalf("indexed range after analyze = %#v", result)
	}
}
