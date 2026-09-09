package blackbox_test

import (
	"reflect"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue265RecreatedTableDoesNotSeeDroppedRows(t *testing.T) {
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
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE t (id INT PRIMARY KEY, val INT)",
		"INSERT INTO t VALUES (1, 100), (2, 200)",
		"DROP TABLE t",
		"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(20))",
		"INSERT INTO t VALUES (1, 'Alice')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	result := client.query("SELECT * FROM t ORDER BY id")
	if result.err != "" {
		t.Fatalf("select after recreate: %#v", result)
	}
	want := [][]string{{"1", "Alice"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows after recreate = %#v, want %#v", result.rows, want)
	}
}
