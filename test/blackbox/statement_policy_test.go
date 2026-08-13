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

// TestMySQLPreparedReadKeepsTextWireContract protects the prepared read after
// it has bound values. It checks only the MySQL wire contract.
func TestMySQLPreparedReadKeepsTextWireContract(t *testing.T) {
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

	text := client.query("SELECT id, name FROM contacts WHERE id = 1")
	prepared := client.prepare("SELECT id, name FROM contacts WHERE id = ?")
	if prepared.err != "" {
		t.Fatalf("prepare read = %#v", prepared)
	}
	defer client.closePrepared(prepared.id)
	bound := client.executePreparedValues(prepared.id, []preparedParameter{{typ: 0x03, value: []byte{1, 0, 0, 0}}})
	if text.err != "" || bound.err != "" || !reflect.DeepEqual(bound.rows, text.rows) || !reflect.DeepEqual(bound.metadata, text.metadata) {
		t.Fatalf("prepared read differs from text read: text=%#v prepared=%#v", text, bound)
	}

	missingPrepared := client.prepare("SELECT * FROM contacts")
	if missingPrepared.err != "" {
		t.Fatalf("prepare missing read = %#v", missingPrepared)
	}
	defer client.closePrepared(missingPrepared.id)
	mustQuery(t, client, "DROP TABLE contacts")
	missingText := client.query("SELECT * FROM contacts")
	missingBound := client.executePrepared(missingPrepared.id)
	if missingText.errCode != 1146 || missingBound.errCode != missingText.errCode || missingBound.errState != missingText.errState || missingBound.err != missingText.err {
		t.Fatalf("prepared missing read differs from text read: text=%#v prepared=%#v", missingText, missingBound)
	}
}
