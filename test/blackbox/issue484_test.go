package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue484UnnamedUniqueKeysUseMySQLNamesThroughMySQL(t *testing.T) {
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
		"CREATE DATABASE issue484",
		"USE issue484",
		"CREATE TABLE n8 (id INT PRIMARY KEY, a INT, b INT, UNIQUE (a), UNIQUE (a, b))",
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	n8 := client.query("SHOW INDEX FROM n8 WHERE Seq_in_index = 1")
	if n8.err != "" || len(n8.rows) != 3 || n8.rows[0][2] != "PRIMARY" || n8.rows[1][2] != "a" || n8.rows[2][2] != "a_2" {
		t.Fatalf("SHOW INDEX FROM n8: %#v", n8)
	}
	p := client.query("SHOW INDEX FROM p")
	if p.err != "" || len(p.rows) != 2 || p.rows[1][2] != "code" {
		t.Fatalf("SHOW INDEX FROM p: %#v", p)
	}
	if result := client.query("ALTER TABLE p DROP INDEX code"); result.err != "" {
		t.Fatalf("ALTER TABLE p DROP INDEX code: %#v", result)
	}
}
