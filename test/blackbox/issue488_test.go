package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue488DropIndexRemovesServerNamedUniqueKeyThroughMySQL(t *testing.T) {
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
		"CREATE DATABASE issue488",
		"USE issue488",
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pid INT, CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES p (id))",
		"CREATE INDEX ic ON c (pid)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	before := client.query("SHOW INDEX FROM p WHERE Key_name = 'p_code_unique'")
	if before.err != "" || len(before.rows) != 1 || before.rows[0][1] != "0" || before.rows[0][4] != "code" {
		t.Fatalf("SHOW INDEX before drop: %#v", before)
	}
	if result := client.query("ALTER TABLE p DROP INDEX p_code_unique"); result.err != "" {
		t.Fatalf("ALTER TABLE p DROP INDEX p_code_unique: %#v", result)
	}
	after := client.query("SHOW INDEX FROM p")
	if after.err != "" || len(after.rows) != 1 || after.rows[0][2] != "PRIMARY" {
		t.Fatalf("SHOW INDEX after drop: %#v", after)
	}
	for _, query := range []string{"INSERT INTO p VALUES (1, 10)", "INSERT INTO p VALUES (2, 10)"} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}
}
