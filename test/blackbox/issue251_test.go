package blackbox_test

import (
	"reflect"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue251OrderResolvesAliasWithinExpression(t *testing.T) {
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
		"CREATE TABLE scores (id INT, a INT, b INT)",
		"INSERT INTO scores VALUES (1, 10, 3), (2, 20, 1), (3, 30, 2)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	result := client.query("SELECT id, a + b AS total FROM scores ORDER BY total + 1 DESC")
	if result.err != "" {
		t.Fatalf("ORDER BY alias within expression: %#v", result)
	}
	want := [][]string{{"3", "32"}, {"2", "21"}, {"1", "13"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("ORDER BY alias within expression rows = %#v, want %#v", result.rows, want)
	}
}