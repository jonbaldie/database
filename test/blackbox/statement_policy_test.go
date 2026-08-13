package blackbox_test

import (
	"reflect"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestMySQLTextReadKeepsWireContract protects the text read that enters the
// statement execution policy. It checks only the MySQL wire contract.
func TestMySQLTextReadKeepsWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, statement := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE contacts (id INT, name VARCHAR(32))",
		"INSERT INTO contacts VALUES (1, 'Ada')",
	} {
		mustQuery(t, client, statement)
	}

	read := client.query("SELECT id, name FROM contacts WHERE id = 1")
	if read.err != "" || !reflect.DeepEqual(read.columns, []string{"id", "name"}) || !reflect.DeepEqual(read.rows, [][]string{{"1", "Ada"}}) {
		t.Fatalf("text read = %#v", read)
	}
	if len(read.metadata) != 2 || read.metadata[0].name != "id" || read.metadata[1].name != "name" {
		t.Fatalf("text read metadata = %#v", read.metadata)
	}

	missing := client.query("SELECT * FROM missing")
	if missing.err == "" || missing.errCode != 1146 {
		t.Fatalf("missing table read = %#v", missing)
	}
}
