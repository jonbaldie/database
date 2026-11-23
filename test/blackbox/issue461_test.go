package blackbox_test

import (
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue461CanonicalDatabaseIdentifiersOnWire(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "identifier-test-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	client := newWireClient(t, address, "admin", "identifier-test-secret")
	defer client.close()
	mustQuery(t, client, "CREATE DATABASE `straße`")
	mustQuery(t, client, "CREATE TABLE `straße`.t (id INT)")
	mustQuery(t, client, "INSERT INTO `straße`.t VALUES (1)")

	rename := client.query("RENAME TABLE `straße`.t TO `STRASSE`.u")
	if rename.err != "" {
		t.Errorf("rename within equivalent namespace: %s", rename.err)
	} else {
		result := client.query("SELECT id FROM `straße`.u")
		if result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" {
			t.Errorf("select renamed row = %#v, want row 1", result)
		}
	}

	mustQuery(t, client, "USE `straße`")
	mustQuery(t, client, "DROP DATABASE `STRASSE`")
	result := client.query("SELECT DATABASE()")
	if result.err != "" || len(result.rows) != 1 || len(result.nulls) != 1 || !result.nulls[0][0] {
		t.Errorf("SELECT DATABASE() after drop = %#v, want NULL", result)
	}
}
