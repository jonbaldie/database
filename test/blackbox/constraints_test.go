package blackbox_test

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestConstraintSurfaceThroughMySQL verifies the public constraint contract
// through a running server and the MySQL wire protocol.
func TestConstraintSurfaceThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		`CREATE TABLE parent (
			id INT NOT NULL,
			code VARCHAR(8) NOT NULL,
			rank_value INT NOT NULL DEFAULT 7,
			PRIMARY KEY (id),
			CONSTRAINT parent_code_unique UNIQUE (code),
			CONSTRAINT parent_rank CHECK (rank_value >= 0)
		)`,
		`CREATE TABLE child (
			id INT,
			parent_id INT,
			label VARCHAR(8) NOT NULL DEFAULT 'new',
			PRIMARY KEY (id),
			CONSTRAINT child_pair_unique UNIQUE (parent_id, label),
			CONSTRAINT child_parent FOREIGN KEY (parent_id) REFERENCES parent (id),
			CONSTRAINT child_id CHECK (id > 0)
		)`,
		"INSERT INTO parent (id, code) VALUES (1, 'P1'), (2, 'P2')",
		"INSERT INTO child (id, parent_id) VALUES (1, 1), (2, NULL), (3, NULL)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}
	if result := client.query("SELECT rank_value FROM parent WHERE id = 1"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "7" {
		t.Fatalf("default value: %#v", result)
	}
	if result := client.query("SHOW CREATE TABLE child"); result.err != "" || len(result.rows) != 1 || !strings.Contains(result.rows[0][1], "CONSTRAINT `child_parent`") {
		t.Fatalf("show create table: %#v", result)
	}

	for _, query := range []string{
		"INSERT INTO parent (id, code) VALUES (3, 'p1')",
		"INSERT INTO child (id, parent_id) VALUES (1, 1)",
		"INSERT INTO child (id, parent_id) VALUES (4, 1)",
		"INSERT INTO child (id, parent_id, label) VALUES (4, 1, NULL)",
		"INSERT INTO child (id, parent_id) VALUES (4, 99)",
		"INSERT INTO child (id, parent_id) VALUES (0, 1)",
		"INSERT INTO child (id, parent_id, label) VALUES (4, 1, 'x'), (5, 99, 'y')",
	} {
		if result := client.query(query); !strings.HasPrefix(result.err, "23000") {
			t.Fatalf("constraint failure for %q: %#v", query, result)
		}
	}
	for _, failure := range []struct {
		query string
		code  uint16
	}{
		{"UPDATE child SET parent_id = 99 WHERE id = 1", 1452},
		{"UPDATE child SET label = NULL WHERE id = 1", 1048},
		{"UPDATE child SET id = 0 WHERE id = 1", 3819},
		{"UPDATE child SET id = 2 WHERE id = 3", 1062},
		{"UPDATE parent SET code = 'p1' WHERE id = 2", 1062},
	} {
		if result := client.query(failure.query); result.errCode != failure.code || !strings.HasPrefix(result.err, "23000") {
			t.Fatalf("update constraint failure for %q: %#v", failure.query, result)
		}
	}
	for _, query := range []string{
		"CREATE TABLE invalid_check (id INT, CHECK (missing > 0))",
		"CREATE TABLE invalid_default (id INT DEFAULT value)",
		"CREATE TABLE invalid_expression_default (id INT DEFAULT 1+1)",
	} {
		if result := client.query(query); result.err == "" {
			t.Fatalf("invalid schema was accepted: query=%q result=%#v", query, result)
		}
	}
	for _, query := range []string{"UPDATE parent SET id = 9 WHERE id = 1", "DELETE FROM parent WHERE id = 1"} {
		if result := client.query(query); result.errCode != 1451 || !strings.HasPrefix(result.err, "23000") {
			t.Fatalf("parent foreign-key failure for %q: %#v", query, result)
		}
	}
	for _, query := range []string{"SELECT id FROM child WHERE id = 4", "SELECT id FROM child WHERE id = 5"} {
		if result := client.query(query); result.err != "" || len(result.rows) != 0 {
			t.Fatalf("failed multi-row write was not atomic: query=%q result=%#v", query, result)
		}
	}

	for _, query := range []string{
		"START TRANSACTION",
		"INSERT INTO child (id, parent_id, label) VALUES (6, 1, 'six')",
		"COMMIT",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("transaction query %q: %#v", query, result)
		}
	}
	if result := client.query("SELECT id FROM child WHERE id = 6"); result.err != "" || len(result.rows) != 1 {
		t.Fatalf("committed constrained write: %#v", result)
	}
	for _, query := range []string{"START TRANSACTION", "INSERT INTO child (id, parent_id, label) VALUES (7, 1, 'seven')"} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("transaction setup %q: %#v", query, result)
		}
	}
	if result := client.query("INSERT INTO child (id, parent_id, label) VALUES (8, 99, 'bad')"); result.errCode != 1452 {
		t.Fatalf("failed transaction statement: %#v", result)
	}
	if result := client.query("COMMIT"); result.err != "" {
		t.Fatalf("commit after failed statement: %#v", result)
	}
	for _, query := range []string{"SELECT id FROM child WHERE id = 7", "SELECT id FROM child WHERE id = 8"} {
		result := client.query(query)
		if result.err != "" || (strings.HasSuffix(query, "= 7") && len(result.rows) != 1) || (strings.HasSuffix(query, "= 8") && len(result.rows) != 0) {
			t.Fatalf("transaction boundary: query=%q result=%#v", query, result)
		}
	}

	for _, query := range []string{
		"CREATE TABLE legacy (id INT, code INT)",
		"INSERT INTO legacy VALUES (1, 7), (2, 7)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("schema-change setup %q: %#v", query, result)
		}
	}
	if result := client.query("ALTER TABLE legacy ADD CONSTRAINT legacy_code_unique UNIQUE (code)"); !strings.HasPrefix(result.err, "23000") {
		t.Fatalf("invalid existing rows accepted a constraint: %#v", result)
	}
	if result := client.query("INSERT INTO legacy VALUES (3, 7)"); result.err != "" {
		t.Fatalf("failed schema change changed the previous definition: %#v", result)
	}
	for _, query := range []string{
		"DELETE FROM legacy WHERE id = 2",
		"DELETE FROM legacy WHERE id = 3",
		"ALTER TABLE legacy ADD CONSTRAINT legacy_code_unique UNIQUE (code)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("valid schema change %q: %#v", query, result)
		}
	}
	if result := client.query("INSERT INTO legacy VALUES (4, 7)"); !strings.HasPrefix(result.err, "23000") {
		t.Fatalf("added constraint did not enforce new rows: %#v", result)
	}
	for _, query := range []string{
		"CREATE TABLE column_rules (id INT)",
		"INSERT INTO column_rules VALUES (1)",
		"ALTER TABLE column_rules ADD COLUMN status INT NOT NULL DEFAULT 3",
		"INSERT INTO column_rules (id) VALUES (2)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("column-rule schema change %q: %#v", query, result)
		}
	}
	if result := client.query("SELECT status FROM column_rules WHERE id = 2"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "3" {
		t.Fatalf("schema-change default: %#v", result)
	}
	if result := client.query("ALTER TABLE column_rules MODIFY COLUMN status INT NOT NULL DEFAULT 4"); result.err != "" {
		t.Fatalf("modify column rule: %#v", result)
	}

	for _, query := range []string{"EXPLAIN FORMAT=JSON INSERT INTO child (id, parent_id) VALUES (7, 1)", "EXPLAIN FORMAT=JSON DELETE FROM parent WHERE id = 1"} {
		if result := client.query(query); result.err != "" || len(result.rows) != 1 || !strings.Contains(result.rows[0][0], `"kind":"constraint_check"`) || !strings.Contains(result.rows[0][0], `"constraint_name":"child_parent"`) {
			t.Fatalf("constraint plan: query=%q result=%#v", query, result)
		}
	}
	if result := client.query("EXPLAIN FORMAT=JSON DELETE FROM parent WHERE id = 1"); result.err != "" || len(result.rows) != 1 || !strings.Contains(result.rows[0][0], `"table":"child"`) {
		t.Fatalf("inbound foreign-key plan has the wrong owner: %#v", result)
	}
}

func TestCompositeForeignKeyValidationThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE parent (account_id INT NOT NULL, item_id INT NOT NULL, PRIMARY KEY (account_id, item_id))",
		"CREATE TABLE child (id INT NOT NULL, account_id INT, item_id INT, PRIMARY KEY (id), CONSTRAINT child_parent FOREIGN KEY (account_id, item_id) REFERENCES parent (account_id, item_id))",
		"INSERT INTO parent VALUES (1, 10), (1, 20)",
		"INSERT INTO child VALUES (1, 1, 10), (2, NULL, 99), (3, 1, NULL)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("query %q: %#v", query, result)
		}
	}
	if result := client.query("INSERT INTO child VALUES (4, 1, 99)"); result.errCode != 1452 || !strings.HasPrefix(result.err, "23000") {
		t.Fatalf("missing composite parent key: %#v", result)
	}
	if result := client.query("SELECT id FROM child"); result.err != "" || len(result.rows) != 3 {
		t.Fatalf("matching and nullable composite keys: %#v", result)
	}
}
