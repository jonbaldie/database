package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue527AlterIndexHidesUniqueConstraintThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	for _, query := range []string{
		"CREATE DATABASE issue527",
		"USE issue527",
		"CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64), CONSTRAINT uq_email UNIQUE (email))",
		"ALTER TABLE accounts ALTER INDEX uq_email INVISIBLE",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}
	client.close()
	_ = process.Stop()
	_ = process.Wait()

	process, address = startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	client = newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	shown := client.query("SHOW INDEX FROM issue527.accounts WHERE Key_name = 'uq_email'")
	if shown.err != "" || len(shown.rows) != 1 || shown.rows[0][13] != "NO" {
		t.Fatalf("SHOW INDEX after restart: %#v", shown)
	}
	if result := client.query("INSERT INTO issue527.accounts VALUES (1, 'a@example.com')"); result.err != "" {
		t.Fatalf("first insert: %#v", result)
	}
	if result := client.query("INSERT INTO issue527.accounts VALUES (2, 'a@example.com')"); result.errCode != 1062 {
		t.Fatalf("duplicate email with invisible uq_email: %#v", result)
	}
}
