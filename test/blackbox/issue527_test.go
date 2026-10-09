package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue527AlterIndexHidesUniqueKeyThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	for _, query := range []string{
		"CREATE DATABASE issue527",
		"USE issue527",
		"CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64), name VARCHAR(64), CONSTRAINT uq_email UNIQUE (email))",
		"CREATE INDEX ix_name ON accounts (name)",
	} {
		mustQuery(t, client, query)
	}
	before := client.query("SHOW INDEX FROM accounts WHERE Key_name = 'uq_email'")
	if before.err != "" || len(before.rows) != 1 || before.rows[0][13] != "YES" {
		t.Fatalf("SHOW INDEX before ALTER INDEX: %#v", before)
	}
	mustQuery(t, client, "ALTER TABLE accounts ALTER INDEX uq_email INVISIBLE")
	mustQuery(t, client, "ALTER TABLE accounts ALTER INDEX ix_name INVISIBLE")
	_ = client.close()
	_ = process.Stop()
	_ = process.Wait()

	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client = newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	mustQuery(t, client, "USE issue527")
	// A visibility change alone must reach the durable catalog.
	for _, index := range []string{"uq_email", "ix_name"} {
		after := client.query("SHOW INDEX FROM accounts WHERE Key_name = '" + index + "'")
		if after.err != "" || len(after.rows) != 1 || after.rows[0][13] != "NO" {
			t.Fatalf("SHOW INDEX for %s after ALTER INDEX and restart: %#v", index, after)
		}
	}
	mustQuery(t, client, "INSERT INTO accounts VALUES (1, 'a@example.com', 'a')")
	if result := client.query("INSERT INTO accounts VALUES (2, 'a@example.com', 'b')"); result.err == "" {
		t.Fatalf("duplicate email accepted while uq_email is invisible: %#v", result)
	}
}
