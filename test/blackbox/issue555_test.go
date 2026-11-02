package blackbox_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue555SessionDefaultsUseAssignmentRules(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := t.TempDir()
	initializeServer(t, runner, directory, "settings-secret")
	instance := startInstance(t, runner, blackbox.InstanceOptions{DataDirectory: directory})
	defer func() { _ = instance.Process.Stop(); _ = instance.Process.Wait() }()
	client := newWireClient(t, instance.MySQLAddress, "admin", "settings-secret")
	defer client.close()

	mustQuery(t, client, "CREATE DATABASE issue555")
	mustQuery(t, client, "USE issue555")
	mustQuery(t, client, "CREATE TABLE items (id INT PRIMARY KEY)")

	t.Run("autocommit default commits", func(t *testing.T) {
		mustQuery(t, client, "SET autocommit = 0")
		mustQuery(t, client, "INSERT INTO items VALUES (1)")
		mustQuery(t, client, "SET autocommit = DEFAULT")
		mustQuery(t, client, "ROLLBACK")
		result := client.query("SELECT id FROM items")
		if result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"1"}}) {
			t.Errorf("row after default and rollback: %#v, want [[1]]", result)
		}
	})
	for _, name := range []string{"transaction_isolation", "transaction_read_only"} {
		t.Run(name+" rejects default in transaction", func(t *testing.T) {
			mustQuery(t, client, "BEGIN")
			result := client.query("SET " + name + " = DEFAULT")
			if !strings.Contains(result.err, "transaction characteristics cannot change in an active transaction") {
				t.Errorf("SET %s = DEFAULT: %#v, want transaction error", name, result)
			}
			mustQuery(t, client, "ROLLBACK")
		})
	}
}
