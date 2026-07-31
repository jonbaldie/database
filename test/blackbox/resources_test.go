package blackbox_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLBoundsOrderedReadsAndPublishesResourceEvidence(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "resource-secret")
	diagnostics, mysqlAddress := freeAddress(t), freeAddress(t)
	process, err := runner.Start(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", mysqlAddress, "--diagnostics-listen-address", diagnostics, "--execution-memory-limit-bytes=50", "--aggregate-execution-memory-limit-bytes=50", "--temporary-storage-limit-bytes=1024", "--aggregate-temporary-storage-limit-bytes=1024", "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	nextReadyEvent(t, process)

	client := newWireClient(t, mysqlAddress, "admin", "resource-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE authors (id INT, name VARCHAR(32))",
		"INSERT INTO authors VALUES (1, 'Ada'), (2, 'Grace'), (3, 'Linus')",
	} {
		mustQuery(t, client, query)
	}

	ordered := client.query("SELECT name FROM authors ORDER BY name DESC")
	if ordered.err != "" || !reflect.DeepEqual(ordered.rows, [][]string{{"Linus"}, {"Grace"}, {"Ada"}}) {
		t.Fatalf("bounded ordered read: %#v", ordered)
	}
	analyzed := client.query("EXPLAIN ANALYZE FORMAT=JSON SELECT name FROM authors ORDER BY name DESC")
	if analyzed.err != "" || len(analyzed.rows) != 1 {
		t.Fatalf("resource analysis: %#v", analyzed)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(analyzed.rows[0][0]), &document); err != nil {
		t.Fatalf("decode resource analysis: %v", err)
	}
	actual := document["plan"].(map[string]any)["actual"].(map[string]any)
	if actual["spill_count"] == float64(0) || actual["temporary_storage_bytes"] == float64(0) {
		t.Fatalf("resource analysis evidence: %#v", actual)
	}

	response, err := http.Get("http://" + diagnostics + "/metrics")
	if err != nil {
		t.Fatalf("read resource diagnostics: %v", err)
	}
	defer response.Body.Close()
	metrics, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(metrics), "# TYPE database_resource_spills_total counter") || strings.Contains(string(metrics), "database_resource_spills_total 0\n") {
		t.Fatalf("resource diagnostics = status %d body %q", response.StatusCode, metrics)
	}
}
