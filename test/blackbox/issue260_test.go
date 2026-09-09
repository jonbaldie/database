package blackbox_test

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue260AlterTableRenamePreservesTable(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE issue260",
		"USE issue260",
		"CREATE TABLE items (id INT PRIMARY KEY, value INT NOT NULL)",
		"CREATE INDEX items_value_idx ON items (value)",
		"INSERT INTO items VALUES (1, 100)",
		"ALTER TABLE items RENAME renamed_items",
	} {
		mustQuery(t, client, query)
	}

	if result := client.query("SELECT value FROM renamed_items WHERE id = 1"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "100" {
		t.Fatalf("renamed table data: %#v", result)
	}
	if result := client.query("SELECT * FROM items"); result.errCode != 1146 {
		t.Fatalf("old table lookup: %#v", result)
	}
	if result := client.query("SHOW CREATE TABLE renamed_items"); result.err != "" || len(result.rows) != 1 || !strings.Contains(result.rows[0][1], "PRIMARY KEY (`id`)") || !strings.Contains(result.rows[0][1], "INDEX `items_value_idx` (`value`)") {
		t.Fatalf("renamed table definition: %#v", result)
	}

	mustQuery(t, client, "ALTER TABLE renamed_items RENAME TO final_items")
	if result := client.query("SELECT id, value FROM final_items"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" || result.rows[0][1] != "100" {
		t.Fatalf("table rename with TO: %#v", result)
	}
}
