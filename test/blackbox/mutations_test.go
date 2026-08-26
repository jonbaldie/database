package blackbox_test

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLReplaceUsesDeleteThenInsertAffectedRows(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE users (id INT NOT NULL, email VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL, PRIMARY KEY (id), UNIQUE INDEX uq_email (email))",
		"INSERT INTO users VALUES (1, 'ada@example.test', 'Ada'), (2, 'bea@example.test', 'Bea')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	if result := client.query("REPLACE INTO users VALUES (1, 'ada-lovelace@example.test', 'Ada Lovelace')"); result.err != "" || result.affected != 2 {
		t.Fatalf("replace primary-key conflict: %#v", result)
	}
	if result := client.query("REPLACE INTO users VALUES (1, 'bea@example.test', 'Ada Byron')"); result.err != "" || result.affected != 3 {
		t.Fatalf("replace multiple unique conflicts: %#v", result)
	}
	if result := client.query("SELECT id, email, name FROM users"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "1,bea@example.test,Ada Byron" {
		t.Fatalf("replace final rows: %#v", result)
	}

	statement := client.prepare("REPLACE INTO users VALUES (?, ?, ?)")
	if statement.err != "" {
		t.Fatalf("prepare replace: %#v", statement)
	}
	prepared := client.executePreparedValues(statement.id, []preparedParameter{
		{typ: 0x08, value: []byte{3, 0, 0, 0, 0, 0, 0, 0}},
		{typ: 0xfd, value: []byte("grace@example.test")},
		{typ: 0xfd, value: []byte("Grace")},
	})
	client.closePrepared(statement.id)
	if prepared.err != "" || prepared.affected != 1 {
		t.Fatalf("prepared replace: %#v", prepared)
	}
}

func TestMySQLReplaceChecksForeignKeyDeletes(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE parents (id INT NOT NULL, name VARCHAR(64) NOT NULL, PRIMARY KEY (id))",
		"CREATE TABLE children (id INT NOT NULL, parent_id INT NOT NULL, PRIMARY KEY (id), CONSTRAINT child_parent FOREIGN KEY (parent_id) REFERENCES parents (id))",
		"INSERT INTO parents VALUES (1, 'Ada')",
		"INSERT INTO children VALUES (1, 1)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	if result := client.query("REPLACE INTO parents VALUES (1, 'Ada Lovelace')"); result.errCode != 1451 || !strings.HasPrefix(result.err, "23000") {
		t.Fatalf("replace parent foreign-key failure: %#v", result)
	}
	if result := client.query("SELECT id, name FROM parents"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "1,Ada" {
		t.Fatalf("failed replacement changed parent row: %#v", result)
	}
}

func TestMySQLInsertOnDuplicateKeyUpdatesAtomically(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE users (id INT NOT NULL, email VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL, PRIMARY KEY (id), UNIQUE INDEX uq_email (email))",
		"INSERT INTO users VALUES (1, 'ada@example.test', 'Ada')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	query := "INSERT INTO users VALUES (1, 'ada-lovelace@example.test', 'Ada Lovelace') ON DUPLICATE KEY UPDATE email = VALUES(email), name = VALUES(name)"
	if result := client.query(query); result.err != "" || result.affected != 2 {
		t.Fatalf("changed duplicate-key update: %#v", result)
	}
	if result := client.query(query); result.err != "" || result.affected != 0 {
		t.Fatalf("unchanged duplicate-key update: %#v", result)
	}
	if result := client.query("SELECT id, email, name FROM users"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "1,ada-lovelace@example.test,Ada Lovelace" {
		t.Fatalf("duplicate-key final rows: %#v", result)
	}

	statement := client.prepare("INSERT INTO users VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE name = VALUES(name)")
	if statement.err != "" {
		t.Fatalf("prepare duplicate-key update: %#v", statement)
	}
	prepared := client.executePreparedValues(statement.id, []preparedParameter{
		{typ: 0x08, value: []byte{1, 0, 0, 0, 0, 0, 0, 0}},
		{typ: 0xfd, value: []byte("ada-lovelace@example.test")},
		{typ: 0xfd, value: []byte("Ada Byron")},
	})
	client.closePrepared(statement.id)
	if prepared.err != "" || prepared.affected != 2 {
		t.Fatalf("prepared duplicate-key update: %#v", prepared)
	}

	failed := client.query("INSERT INTO users VALUES (2, 'grace@example.test', 'Grace'), (1, 'grace@example.test', 'Rejected') ON DUPLICATE KEY UPDATE email = VALUES(email), name = VALUES(name)")
	if failed.errCode != 1062 {
		t.Fatalf("duplicate-key constraint failure: %#v", failed)
	}
	if result := client.query("SELECT id, email, name FROM users"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "1,ada-lovelace@example.test,Ada Byron" {
		t.Fatalf("failed duplicate-key update leaked changes: %#v", result)
	}
}

func TestMySQLInsertSelectSnapshotsTheSourceRows(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE source_records (id INT NOT NULL, name VARCHAR(64) NOT NULL)",
		"CREATE TABLE copied_records (id INT NOT NULL, name VARCHAR(64) NOT NULL, PRIMARY KEY (id))",
		"INSERT INTO source_records VALUES (1, 'Ada'), (2, 'Grace'), (3, 'Linus')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	inserted := client.query("INSERT INTO copied_records (id, name) SELECT id, name FROM source_records WHERE id > 1")
	if inserted.err != "" || inserted.affected != 2 {
		t.Fatalf("insert-select: %#v", inserted)
	}
	if result := client.query("SELECT id, name FROM copied_records ORDER BY id"); result.err != "" || len(result.rows) != 2 || strings.Join(result.rows[0], ",") != "2,Grace" || strings.Join(result.rows[1], ",") != "3,Linus" {
		t.Fatalf("insert-select rows: %#v", result)
	}
	if result := client.query("INSERT INTO source_records (id, name) SELECT id + 10, name FROM source_records WHERE id = 1"); result.err != "" || result.affected != 1 {
		t.Fatalf("same-table insert-select: %#v", result)
	}
	if result := client.query("SELECT id, name FROM source_records WHERE id = 11"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "11,Ada" {
		t.Fatalf("same-table insert-select did not use the source snapshot: %#v", result)
	}

	statement := client.prepare("INSERT INTO copied_records (id, name) SELECT id + 10, name FROM source_records WHERE id > ?")
	if statement.err != "" {
		t.Fatalf("prepare insert-select: %#v", statement)
	}
	prepared := client.executePreparedValues(statement.id, []preparedParameter{{typ: 0x08, value: []byte{2, 0, 0, 0, 0, 0, 0, 0}}})
	client.closePrepared(statement.id)
	if prepared.err != "" || prepared.affected != 2 {
		t.Fatalf("prepared insert-select: %#v", prepared)
	}
	if failed := client.query("INSERT INTO copied_records (id) SELECT id, name FROM source_records"); failed.errCode != 1136 {
		t.Fatalf("insert-select column mismatch: %#v", failed)
	}
	if result := client.query("REPLACE INTO copied_records SELECT id, name FROM source_records WHERE id = 2"); result.err != "" || result.affected != 2 {
		t.Fatalf("replace-select: %#v", result)
	}
	if result := client.query("UPDATE source_records SET name = 'Linus Torvalds' WHERE id = 3"); result.err != "" || result.affected != 1 {
		t.Fatalf("update insert-select source: %#v", result)
	}
	if result := client.query("INSERT INTO copied_records SELECT id, name FROM source_records WHERE id = 3 ON DUPLICATE KEY UPDATE name = VALUES(name)"); result.err != "" || result.affected != 2 {
		t.Fatalf("upsert-select: %#v", result)
	}
}

func TestMySQLInsertAndReplaceSetForms(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE users (id INT NOT NULL, name VARCHAR(64) NOT NULL, PRIMARY KEY (id))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}
	if result := client.query("INSERT INTO users SET id = 1, name = 'Ada'"); result.err != "" || result.affected != 1 {
		t.Fatalf("insert-set: %#v", result)
	}
	if result := client.query("REPLACE users SET id = 1, name = 'Ada Lovelace'"); result.err != "" || result.affected != 2 {
		t.Fatalf("replace-set: %#v", result)
	}
	statement := client.prepare("INSERT INTO users SET id = ?, name = ?")
	if statement.err != "" {
		t.Fatalf("prepare insert-set: %#v", statement)
	}
	prepared := client.executePreparedValues(statement.id, []preparedParameter{
		{typ: 0x08, value: []byte{2, 0, 0, 0, 0, 0, 0, 0}},
		{typ: 0xfd, value: []byte("Grace")},
	})
	client.closePrepared(statement.id)
	if prepared.err != "" || prepared.affected != 1 {
		t.Fatalf("prepared insert-set: %#v", prepared)
	}
	if result := client.query("INSERT INTO users VALUE (3, 'Linus')"); result.err != "" || result.affected != 1 {
		t.Fatalf("insert-value: %#v", result)
	}
	if result := client.query("INSERT INTO users SET id = 4, name = 'Rejected' RETURNING name"); result.errCode != 1235 {
		t.Fatalf("insert returning was accepted: %#v", result)
	}
	if result := client.query("INSERT INTO users VALUES (1, 'Ignored') ON DUPLICATE KEY UPDATE name = CONCAT('Ignored')"); result.errCode != 1235 {
		t.Fatalf("upsert expression was accepted: %#v", result)
	}
	if result := client.query("SELECT id, name FROM users ORDER BY id"); result.err != "" || len(result.rows) != 3 || strings.Join(result.rows[0], ",") != "1,Ada Lovelace" || strings.Join(result.rows[1], ",") != "2,Grace" || strings.Join(result.rows[2], ",") != "3,Linus" {
		t.Fatalf("set final rows: %#v", result)
	}
}

func TestMySQLMutationFormsHavePublicPlans(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE users (id INT NOT NULL, name VARCHAR(64) NOT NULL, PRIMARY KEY (id))",
		"CREATE TABLE source_users (id INT NOT NULL, name VARCHAR(64) NOT NULL)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	plan := client.query("EXPLAIN FORMAT=JSON REPLACE INTO users VALUES (1, 'Ada')")
	if plan.err != "" || len(plan.rows) != 1 || len(plan.rows[0]) != 1 {
		t.Fatalf("replace plan: %#v", plan)
	}
	for _, required := range []string{`"kind":"replace"`, `"mutation_type":"replace"`, `"mutation_type":"delete"`, `"mutation_type":"insert"`} {
		if !strings.Contains(plan.rows[0][0], required) {
			t.Fatalf("replace plan lacks %q: %#v", required, plan)
		}
	}
	upsert := client.query("EXPLAIN FORMAT=JSON INSERT INTO users VALUES (1, 'Ada') ON DUPLICATE KEY UPDATE name = VALUES(name)")
	if upsert.err != "" || len(upsert.rows) != 1 || !strings.Contains(upsert.rows[0][0], `"mutation_type":"upsert"`) {
		t.Fatalf("upsert plan: %#v", upsert)
	}
	insertSelect := client.query("EXPLAIN FORMAT=JSON INSERT INTO users (id, name) SELECT id, name FROM source_users")
	if insertSelect.err != "" || len(insertSelect.rows) != 1 || !strings.Contains(insertSelect.rows[0][0], `"kind":"scan"`) {
		t.Fatalf("insert-select plan: %#v", insertSelect)
	}
}

func TestMySQLExtendedMutationsRespectTransactionVisibility(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	writer := newWireClient(t, address, "admin", "lifecycle-secret")
	defer writer.close()
	observer := newWireClient(t, address, "admin", "lifecycle-secret")
	defer observer.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE users (id INT NOT NULL, name VARCHAR(64) NOT NULL, PRIMARY KEY (id))",
		"CREATE TABLE source_users (id INT NOT NULL, name VARCHAR(64) NOT NULL)",
		"INSERT INTO source_users VALUES (2, 'Grace')",
	} {
		if result := writer.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}
	if result := observer.query("USE app"); result.err != "" {
		t.Fatalf("observer selects app: %#v", result)
	}
	for _, query := range []string{
		"BEGIN",
		"REPLACE INTO users VALUES (1, 'Ada')",
		"INSERT INTO users VALUES (1, 'Ada Lovelace') ON DUPLICATE KEY UPDATE name = VALUES(name)",
		"INSERT INTO users SELECT id, name FROM source_users",
	} {
		if result := writer.query(query); result.err != "" {
			t.Fatalf("transaction query %q: %#v", query, result)
		}
	}
	if result := observer.query("SELECT id FROM users"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("uncommitted extended mutations became visible: %#v", result)
	}
	if result := writer.query("COMMIT"); result.err != "" {
		t.Fatalf("commit extended mutations: %#v", result)
	}
	if result := observer.query("SELECT id, name FROM users ORDER BY id"); result.err != "" || len(result.rows) != 2 || strings.Join(result.rows[0], ",") != "1,Ada Lovelace" || strings.Join(result.rows[1], ",") != "2,Grace" {
		t.Fatalf("committed extended mutations: %#v", result)
	}
	for _, query := range []string{
		"BEGIN",
		"REPLACE INTO users VALUES (1, 'Rejected')",
		"ROLLBACK",
	} {
		if result := writer.query(query); result.err != "" {
			t.Fatalf("rollback query %q: %#v", query, result)
		}
	}
	if result := observer.query("SELECT id, name FROM users WHERE id = 1"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "1,Ada Lovelace" {
		t.Fatalf("rolled-back replace became visible: %#v", result)
	}
}

func TestMySQLReplaceThenPointUpdateChangesTheReplacedRow(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE items (id INT PRIMARY KEY, note VARCHAR(32) NOT NULL)",
		"INSERT INTO items VALUES (1, 'n1'), (2, 'n2')",
		"REPLACE INTO items VALUES (1, 'r2')",
		"UPDATE items SET note = 'updated' WHERE id = 1",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("query %q: %#v", query, result)
		}
	}
	result := client.query("SELECT id, note FROM items ORDER BY id")
	if result.err != "" || len(result.rows) != 2 || strings.Join(result.rows[0], ",") != "1,updated" || strings.Join(result.rows[1], ",") != "2,n2" {
		t.Fatalf("replace then point update: %#v", result)
	}
}
