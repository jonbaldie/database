package blackbox_test

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestSavepointsThroughMySQL verifies savepoint behavior through a running
// server and the MySQL wire protocol.
func TestSavepointsThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE savepoints",
		"USE savepoints",
		"CREATE TABLE entries (id INT PRIMARY KEY)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}
	observer := newWireClient(t, address, "admin", "lifecycle-secret")
	defer observer.close()
	if result := observer.query("USE savepoints"); result.err != "" {
		t.Fatalf("observer namespace: %#v", result)
	}

	for _, query := range []string{
		"BEGIN",
		"INSERT INTO entries VALUES (1)",
		"SAVEPOINT first",
		"INSERT INTO entries VALUES (2)",
		"SAVEPOINT second",
		"INSERT INTO entries VALUES (3)",
		"ROLLBACK WORK TO second",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("savepoint query %q: %#v", query, result)
		}
	}
	if result := client.query("SELECT id FROM entries ORDER BY id"); result.err != "" || len(result.rows) != 2 || strings.Join(result.rows[0], ",") != "1" || strings.Join(result.rows[1], ",") != "2" {
		t.Fatalf("inner savepoint rollback: %#v", result)
	}
	if result := client.query("ROLLBACK TO SAVEPOINT first"); result.err != "" {
		t.Fatalf("outer savepoint rollback: %#v", result)
	}
	if result := client.query("ROLLBACK TO second"); result.errCode != 1305 || !strings.HasPrefix(result.err, "42000") {
		t.Fatalf("later savepoint survived rollback: %#v", result)
	}
	if result := client.query("RELEASE SAVEPOINT first"); result.err != "" {
		t.Fatalf("release savepoint: %#v", result)
	}
	if result := client.query("ROLLBACK TO first"); result.errCode != 1305 || !strings.HasPrefix(result.err, "42000") {
		t.Fatalf("released savepoint survived: %#v", result)
	}
	if result := client.query("SAVEPOINT recover"); result.err != "" {
		t.Fatalf("savepoint before failed statement: %#v", result)
	}
	if result := client.query("INSERT INTO entries VALUES (1)"); result.errCode != 1062 || !strings.HasPrefix(result.err, "23000") {
		t.Fatalf("duplicate statement failure: %#v", result)
	}
	if result := client.query("ROLLBACK TO recover"); result.err != "" {
		t.Fatalf("statement failure released savepoint: %#v", result)
	}
	if result := client.query("INSERT INTO entries VALUES (4)"); result.err != "" {
		t.Fatalf("statement failure ended transaction: %#v", result)
	}
	if result := client.query("COMMIT"); result.err != "" {
		t.Fatalf("commit savepoint transaction: %#v", result)
	}
	if result := client.query("SELECT id FROM entries ORDER BY id"); result.err != "" || len(result.rows) != 2 || strings.Join(result.rows[0], ",") != "1" || strings.Join(result.rows[1], ",") != "4" {
		t.Fatalf("committed savepoint state: %#v", result)
	}

	for _, query := range []string{
		"BEGIN",
		"SAVEPOINT outer",
		"INSERT INTO entries VALUES (5)",
		"SAVEPOINT inner",
		"INSERT INTO entries VALUES (6)",
		"SAVEPOINT outer",
		"ROLLBACK TO inner",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("replacement query %q: %#v", query, result)
		}
	}
	if result := client.query("ROLLBACK TO outer"); result.errCode != 1305 || !strings.HasPrefix(result.err, "42000") {
		t.Fatalf("replacement savepoint survived later rollback: %#v", result)
	}
	if result := client.query("COMMIT"); result.err != "" {
		t.Fatalf("commit replacement transaction: %#v", result)
	}
	if result := client.query("SELECT id FROM entries ORDER BY id"); result.err != "" || len(result.rows) != 3 || strings.Join(result.rows[2], ",") != "5" {
		t.Fatalf("replacement savepoint state: %#v", result)
	}

	for _, query := range []string{
		"BEGIN",
		"INSERT INTO entries VALUES (7)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("conflict query %q: %#v", query, result)
		}
	}
	if result := observer.query("INSERT INTO entries VALUES (8)"); result.err != "" {
		t.Fatalf("concurrent committed write: %#v", result)
	}
	if result := client.query("COMMIT"); result.errCode != 1213 || !strings.HasPrefix(result.err, "40001") {
		t.Fatalf("transaction conflict identity: %#v", result)
	}
	if result := client.query("SELECT id FROM entries ORDER BY id"); result.err != "" || len(result.rows) != 4 || strings.Join(result.rows[3], ",") != "8" {
		t.Fatalf("failed transaction left work: %#v", result)
	}

	if result := client.query("SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED"); result.err != "" {
		t.Fatalf("set read committed: %#v", result)
	}
	for _, query := range []string{
		"BEGIN",
		"INSERT INTO entries VALUES (9)",
		"SAVEPOINT retained",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("read-committed savepoint query %q: %#v", query, result)
		}
	}
	if result := observer.query("INSERT INTO entries VALUES (10)"); result.err != "" {
		t.Fatalf("read-committed concurrent write: %#v", result)
	}
	if result := client.query("INSERT INTO entries VALUES (11)"); result.err != "" {
		t.Fatalf("read-committed later write: %#v", result)
	}
	if result := client.query("ROLLBACK TO retained"); result.err != "" {
		t.Fatalf("read-committed rollback to savepoint: %#v", result)
	}
	if result := client.query("COMMIT"); result.errCode != 1213 || !strings.HasPrefix(result.err, "40001") {
		t.Fatalf("read-committed rollback conflict identity: %#v", result)
	}
	if result := observer.query("SELECT id FROM entries ORDER BY id"); result.err != "" || len(result.rows) != 5 || strings.Join(result.rows[4], ",") != "10" {
		t.Fatalf("savepoint rollback lost concurrent work: %#v", result)
	}
}
