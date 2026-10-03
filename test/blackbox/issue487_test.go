package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue487ShowIndexListsPrimaryFirstThroughMySQL(t *testing.T) {
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
		"CREATE DATABASE issue487",
		"USE issue487",
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pid INT, CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES p (id))",
		"CREATE INDEX ic ON c (pid)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	indexes := client.query("SHOW INDEX FROM c")
	if indexes.err != "" || len(indexes.rows) != 2 || indexes.rows[0][2] != "PRIMARY" || indexes.rows[0][4] != "id" || indexes.rows[1][2] != "ic" || indexes.rows[1][4] != "pid" {
		t.Fatalf("SHOW INDEX FROM c: %#v", indexes)
	}
}
