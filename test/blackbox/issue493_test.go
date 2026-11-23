package blackbox_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue493ExplainAnalyzeRespectsExecutionMemoryLimit(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE issue493",
		"USE issue493",
		"CREATE TABLE n (id INT PRIMARY KEY)",
		"CREATE DATABASE lb",
		"CREATE TABLE lb.n (id INT PRIMARY KEY, pad VARCHAR(40))",
	} {
		mustQuery(t, client, query)
	}

	values := make([]string, 316)
	for index := range values {
		values[index] = fmt.Sprintf("(%d)", index+1)
	}
	mustQuery(t, client, "INSERT INTO n VALUES "+strings.Join(values, ","))

	padding := strings.Repeat("x", 24)
	fullValues := make([]string, 1280)
	for index := range fullValues {
		id := index + 1
		fullValues[index] = fmt.Sprintf("(%d, 'pad-%06d-%s')", id, id, padding)
	}
	mustQuery(t, client, "INSERT INTO lb.n VALUES "+strings.Join(fullValues, ","))
	mustQuery(t, client, "SET execution_memory_limit_bytes = 100000")

	assertIssue493AnalyzesQuery(t, client, "SELECT a.id,b.id FROM issue493.n a CROSS JOIN issue493.n b WHERE a.id<50", 15484)
	assertIssue493AnalyzesQuery(t, client, "SELECT a.id,b.id FROM lb.n a CROSS JOIN lb.n b WHERE a.id<50", 62720)
}

func assertIssue493AnalyzesQuery(t *testing.T, client *wireClient, query string, expectedRows int) {
	t.Helper()
	selected := client.query(query)
	if selected.err != "" || len(selected.rows) != expectedRows {
		t.Fatalf("SELECT under memory limit returned %d rows, error %q (code %d); want %d rows and no error", len(selected.rows), selected.err, selected.errCode, expectedRows)
	}

	analyzed := client.query("EXPLAIN ANALYZE " + query)
	if analyzed.err != "" {
		t.Fatalf("EXPLAIN ANALYZE for successful SELECT failed with error %q (code %d)", analyzed.err, analyzed.errCode)
	}
	actualRowsColumn := slices.Index(analyzed.columns, "actual_rows")
	if actualRowsColumn < 0 || len(analyzed.rows) == 0 || analyzed.rows[0][actualRowsColumn] != fmt.Sprint(expectedRows) {
		t.Fatalf("EXPLAIN ANALYZE runtime rows: %#v, want %d output rows", analyzed, expectedRows)
	}
}
