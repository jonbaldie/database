package blackbox_test

import (
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIdentifierEquivalentDatabaseDDL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "identifier-ddl-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", password)
	defer client.close()
	t.Run("rename", func(t *testing.T) {
		mustQuery(t, client, "CREATE DATABASE `straße`")
		mustQuery(t, client, "CREATE TABLE `straße`.t (id INT)")
		mustQuery(t, client, "RENAME TABLE `straße`.t TO `STRASSE`.u")
		mustQuery(t, client, "SELECT * FROM `straße`.u")
	})
	t.Run("drop", func(t *testing.T) {
		mustQuery(t, client, "USE `straße`")
		mustQuery(t, client, "DROP DATABASE `STRASSE`")
		got := client.query("SELECT DATABASE()")
		if got.err != "" || len(got.nulls) != 1 || len(got.nulls[0]) != 1 || !got.nulls[0][0] {
			t.Fatalf("SELECT DATABASE() after drop = %#v, want NULL", got)
		}
	})
}
