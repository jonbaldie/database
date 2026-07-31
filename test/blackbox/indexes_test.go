package blackbox_test

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestMySQLBTreeIndexesUseThePublicWireContract verifies durable index DDL,
// metadata, uniqueness, hints, and explanation through the running server.
func TestMySQLBTreeIndexesUseThePublicWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		`CREATE TABLE accounts (
			id INT NOT NULL,
			email VARCHAR(64) NOT NULL,
			status VARCHAR(16) NOT NULL,
			created INT NOT NULL,
			PRIMARY KEY (id),
			UNIQUE INDEX uq_email (email),
			INDEX idx_status_created (status ASC, created DESC),
			INDEX idx_email_prefix (email(8)) INVISIBLE,
			INDEX idx_lower_email ((LOWER(email)))
		)`,
		"INSERT INTO accounts VALUES (1, 'ada@example.test', 'open', 2), (2, 'bea@example.test', 'open', 3), (3, 'cam@example.test', 'closed', 1)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	if result := client.query("INSERT INTO accounts VALUES (4, 'ada@example.test', 'open', 4)"); result.errCode != 1062 || !strings.HasPrefix(result.err, "23000") {
		t.Fatalf("unique index did not reject a duplicate: %#v", result)
	}
	if result := client.query("CREATE INDEX idx_created USING BTREE ON accounts (created DESC)"); result.err != "" {
		t.Fatalf("create index: %#v", result)
	}
	if result := client.query("ALTER TABLE accounts ADD INDEX idx_created_status (created, status)"); result.err != "" {
		t.Fatalf("alter table add index: %#v", result)
	}

	show := client.query("SHOW INDEX FROM accounts")
	if show.err != "" || strings.Join(show.columns, ",") != "Table,Non_unique,Key_name,Seq_in_index,Column_name,Collation,Cardinality,Sub_part,Packed,Null,Index_type,Comment,Index_comment,Visible,Expression" {
		t.Fatalf("show index shape: %#v", show)
	}
	if !showIndexContains(show, "PRIMARY", "id", "A", "YES") || !showIndexContains(show, "idx_status_created", "status", "A", "YES") || !showIndexContains(show, "idx_status_created", "created", "D", "YES") || !showIndexContains(show, "idx_email_prefix", "email", "A", "NO") || !showIndexContains(show, "idx_lower_email", "", "A", "YES") {
		t.Fatalf("show index rows: %#v", show)
	}

	create := client.query("SHOW CREATE TABLE accounts")
	for _, required := range []string{"UNIQUE INDEX `uq_email`", "INDEX `idx_status_created` (`status`, `created` DESC)", "INDEX `idx_email_prefix` (`email`(8)) INVISIBLE", "INDEX `idx_lower_email` ((LOWER(email)))"} {
		if create.err != "" || len(create.rows) != 1 || !strings.Contains(create.rows[0][1], required) {
			t.Fatalf("show create missing %q: %#v", required, create)
		}
	}
	if result := client.query("SELECT id FROM accounts USE INDEX (idx_email_prefix) WHERE email = 'ada@example.test'"); result.errCode != 3522 {
		t.Fatalf("invisible index hint was accepted: %#v", result)
	}

	if result := client.query("ALTER TABLE accounts ALTER INDEX idx_email_prefix VISIBLE"); result.err != "" {
		t.Fatalf("make index visible: %#v", result)
	}
	if result := client.query("SHOW INDEX FROM accounts"); result.err != "" || !showIndexContains(result, "idx_email_prefix", "email", "A", "YES") {
		t.Fatalf("visible index metadata: %#v", result)
	}
	if result := client.query("SELECT id FROM accounts FORCE INDEX (idx_status_created) WHERE status = 'open' ORDER BY created DESC"); result.err != "" || strings.Join(result.rows[0], ",") != "2" || strings.Join(result.rows[1], ",") != "1" {
		t.Fatalf("forced index query: %#v", result)
	}
	explain := client.query("EXPLAIN FORMAT=JSON SELECT id FROM accounts FORCE INDEX (idx_status_created) WHERE status = 'open'")
	for _, required := range []string{`"source":"index"`, `"name":"btree_index_scan"`, `"selected":"idx_status_created"`, `"statistics"`, `"opportunities"`} {
		if explain.err != "" || len(explain.rows) != 1 || !strings.Contains(explain.rows[0][0], required) {
			t.Fatalf("index explanation missing %q: %#v", required, explain)
		}
	}
	covering := client.query("EXPLAIN FORMAT=JSON SELECT status, created FROM accounts FORCE INDEX (idx_status_created) WHERE status = 'open'")
	if covering.err != "" || len(covering.rows) != 1 || !strings.Contains(covering.rows[0][0], `"name":"btree_covering_index_scan"`) {
		t.Fatalf("covering index explanation: %#v", covering)
	}
	orderHint := client.query("EXPLAIN FORMAT=JSON SELECT status FROM accounts USE INDEX FOR ORDER BY (idx_status) ORDER BY status")
	if orderHint.err != "" || len(orderHint.rows) != 1 || !strings.Contains(orderHint.rows[0][0], `"selected":"idx_status_created"`) {
		t.Fatalf("order index hint: %#v", orderHint)
	}
	ignoreHint := client.query("EXPLAIN FORMAT=JSON SELECT id FROM accounts IGNORE INDEX (idx_status_created) WHERE status = 'open'")
	if ignoreHint.err != "" || len(ignoreHint.rows) != 1 || strings.Contains(ignoreHint.rows[0][0], `"source":"index"`) {
		t.Fatalf("ignore index hint: %#v", ignoreHint)
	}
	orderScope := client.query("EXPLAIN FORMAT=JSON SELECT id FROM accounts IGNORE INDEX FOR ORDER BY (idx_status_created) WHERE status = 'open'")
	if orderScope.err != "" || len(orderScope.rows) != 1 || !strings.Contains(orderScope.rows[0][0], `"selected":"idx_status_created"`) {
		t.Fatalf("order index hint changed a filter path: %#v", orderScope)
	}
	if result := client.query("SELECT id FROM accounts USE INDEX (no_such_index) WHERE status = 'open'"); result.errCode != 1176 {
		t.Fatalf("invalid hint was accepted: %#v", result)
	}
	if result := client.query("CREATE INDEX missing ON accounts (missing)"); result.errCode != 1072 {
		t.Fatalf("invalid index column was accepted: %#v", result)
	}
	if result := client.query("DROP INDEX idx_created ON accounts"); result.err != "" {
		t.Fatalf("drop index: %#v", result)
	}
	if result := client.query("ALTER TABLE accounts DROP KEY idx_created_status"); result.err != "" {
		t.Fatalf("alter table drop index: %#v", result)
	}
	for _, query := range []string{
		"CREATE TABLE prefix_keys (value VARCHAR(16), UNIQUE INDEX uq_prefix (value(3)))",
		"INSERT INTO prefix_keys VALUES ('abc-one')",
		"CREATE TABLE functional_keys (value VARCHAR(16), UNIQUE INDEX uq_lower ((LOWER(value))))",
		"INSERT INTO functional_keys VALUES ('ABC')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("index behavior setup %q: %#v", query, result)
		}
	}
	for _, query := range []string{
		"INSERT INTO prefix_keys VALUES ('abc-two')",
		"INSERT INTO functional_keys VALUES ('abc')",
	} {
		if result := client.query(query); result.errCode != 1062 {
			t.Fatalf("index behavior did not enforce %q: %#v", query, result)
		}
	}
}

func showIndexContains(result wireResult, index, column, collation, visible string) bool {
	for _, row := range result.rows {
		if len(row) >= 15 && row[2] == index && row[4] == column && row[5] == collation && row[13] == visible {
			return true
		}
	}
	return false
}
