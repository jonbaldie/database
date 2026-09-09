package mysql

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func ddlExecutorForTest(t *testing.T) *textStatementExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	return &textStatementExecutor{session: &session{server: server, database: "app", initialDB: "app", timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{}}}
}

func TestTableDefinitionEvolutionThroughSQL(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE users (id INT, name VARCHAR(32))",
		"INSERT INTO users VALUES (1, 'Ada')",
		"ALTER TABLE users ADD COLUMN active BOOLEAN",
		"ALTER TABLE users RENAME COLUMN name TO display_name",
		"ALTER TABLE users MODIFY COLUMN id BIGINT",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, display_name FROM users")
	if err != nil || len(result.rows) != 1 || !equalRows(result.rows, [][]string{{"1", "Ada"}}) {
		t.Fatalf("evolved rows = %#v, err = %v", result, err)
	}
	if _, err := executeStatement(executor, "TRUNCATE TABLE users"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	result, err = executeStatement(executor, "SELECT * FROM users")
	if err != nil || len(result.rows) != 0 {
		t.Fatalf("truncated rows = %#v, err = %v", result.rows, err)
	}
	for _, query := range []string{"RENAME TABLE users TO accounts", "DROP TABLE accounts", "DROP TABLE IF EXISTS accounts"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	for _, query := range []string{"CREATE DATABASE scratch", "USE scratch", "CREATE TABLE entries (id INT)", "DROP DATABASE scratch", "DROP DATABASE IF EXISTS scratch"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "USE scratch"); err == nil {
		t.Fatal("dropped database remains selectable")
	}
}

func TestModifyColumnPreservesStoredStringValues(t *testing.T) {
	cases := []struct {
		name      string
		setup     []string
		alter     string
		selectSQL string
		want      [][]string
	}{
		{
			name:      "widen_varchar",
			setup:     []string{"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(10) NOT NULL)", "INSERT INTO t VALUES (1, 'alice')"},
			alter:     "ALTER TABLE t MODIFY name VARCHAR(20)",
			selectSQL: "SELECT id, name FROM t",
			want:      [][]string{{"1", "alice"}},
		},
		{
			name:      "change_column",
			setup:     []string{"CREATE TABLE t (name VARCHAR(10))", "INSERT INTO t VALUES ('alice')"},
			alter:     "ALTER TABLE t CHANGE name label VARCHAR(20)",
			selectSQL: "SELECT label FROM t",
			want:      [][]string{{"alice"}},
		},
		{
			name:      "numeric_looking_text",
			setup:     []string{"CREATE TABLE t (name VARCHAR(10))", "INSERT INTO t VALUES ('42')"},
			alter:     "ALTER TABLE t MODIFY name VARCHAR(20)",
			selectSQL: "SELECT name FROM t",
			want:      [][]string{{"42"}},
		},
		{
			name:      "null_value",
			setup:     []string{"CREATE TABLE t (name VARCHAR(10))", "INSERT INTO t VALUES (NULL)"},
			alter:     "ALTER TABLE t MODIFY name VARCHAR(20)",
			selectSQL: "SELECT name FROM t",
			want:      [][]string{{"NULL"}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range test.setup {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("execute %q: %v", query, err)
				}
			}
			if _, err := executeStatement(executor, test.alter); err != nil {
				t.Fatalf("alter %q: %v", test.alter, err)
			}
			result, err := executeStatement(executor, test.selectSQL)
			if err != nil || !equalRows(result.rows, test.want) {
				t.Fatalf("rows = %#v, err = %v, want %#v", result.rows, err, test.want)
			}
		})
	}
}

func TestModifyColumnRejectsIncompatibleStoredString(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (name VARCHAR(10))",
		"INSERT INTO t VALUES ('alice')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	_, err := executeStatement(executor, "ALTER TABLE t MODIFY name VARCHAR(3)")
	if err == nil {
		t.Fatal("too-long stored value accepted")
	}
	if !strings.Contains(err.Error(), "Data too long") {
		t.Fatalf("error = %v, want data too long", err)
	}
}

func TestTableDefinitionEvolutionFailureLeavesCatalogUnchanged(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"CREATE TABLE users (id INT)", "INSERT INTO users VALUES (2)"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "ALTER TABLE users MODIFY COLUMN id TINYINT"); err != nil {
		t.Fatalf("valid narrowing alter: %v", err)
	}
	if _, err := executeStatement(executor, "ALTER TABLE users MODIFY COLUMN id BIT(1)"); err == nil {
		t.Fatal("invalid existing value accepted")
	}
	definition := executor.server.config.Catalog.Snapshot()
	table := definition.Namespaces["app"].Tables["users"]
	if !equalStrings(table.Columns, []string{"id"}) || !equalStrings(table.ColumnTypes, []string{"TINYINT"}) || !equalRows(table.Rows, [][]string{{"2"}}) {
		t.Fatalf("failed alter changed catalog: %#v", table)
	}
}

func TestTableDefinitionRejectsUnsupportedColumnPosition(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"CREATE TABLE users (id INT)", "INSERT INTO users VALUES (2)"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "ALTER TABLE users ADD COLUMN name VARCHAR(32) AFTER id"); err == nil {
		t.Fatal("unsupported column position accepted")
	}
	table := executor.server.config.Catalog.Snapshot().Namespaces["app"].Tables["users"]
	if !equalStrings(table.Columns, []string{"id"}) || !equalRows(table.Rows, [][]string{{"2"}}) {
		t.Fatalf("rejected alter changed catalog: %#v", table)
	}
}

func TestApplyTableDefinitionActionsAtomically(t *testing.T) {
	table := catalog.Table{
		Name:        "users",
		Columns:     []string{"id", "name"},
		ColumnTypes: []string{"INT", "VARCHAR(32)"},
		Rows:        [][]string{{"1", "Ada"}},
	}
	actions := []ddlAction{
		{kind: ddlAddColumn, name: "active", typeName: "BOOLEAN"},
		{kind: ddlRenameColumn, name: "name", newName: "display_name"},
		{kind: ddlModifyColumn, name: "id", typeName: "BIGINT"},
		{kind: ddlDropColumn, name: "active"},
	}

	updated, err := applyTableDefinitionActions(table, actions)
	if err != nil {
		t.Fatalf("apply DDL actions: %v", err)
	}
	if got, want := updated.Columns, []string{"id", "display_name"}; !equalStrings(got, want) {
		t.Fatalf("columns = %#v, want %#v", got, want)
	}
	if got, want := updated.ColumnTypes, []string{"BIGINT", "VARCHAR(32)"}; !equalStrings(got, want) {
		t.Fatalf("column types = %#v, want %#v", got, want)
	}
	if got, want := updated.Rows, [][]string{{"1", "Ada"}}; !equalRows(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
}

func TestApplyTableDefinitionActionsRejectsInvalidChangeWithoutMutation(t *testing.T) {
	table := catalog.Table{
		Name:        "users",
		Columns:     []string{"id"},
		ColumnTypes: []string{"INT"},
		Rows:        [][]string{{"not-an-int"}},
	}

	if _, err := applyTableDefinitionActions(table, []ddlAction{{kind: ddlModifyColumn, name: "id", typeName: "BIGINT"}}); err == nil {
		t.Fatal("invalid existing row accepted")
	}
	if got, want := table.Columns, []string{"id"}; !equalStrings(got, want) {
		t.Fatalf("original columns changed: %#v", got)
	}
	if got, want := table.Rows, [][]string{{"not-an-int"}}; !equalRows(got, want) {
		t.Fatalf("original rows changed: %#v", got)
	}
}

func TestApplyTableDefinitionActionsPreservesNullDuringTypeChange(t *testing.T) {
	table := catalog.Table{
		Name:        "values",
		Columns:     []string{"value"},
		ColumnTypes: []string{"INT"},
		Rows:        [][]string{{storedSQLNullValue}},
	}
	updated, err := applyTableDefinitionActions(table, []ddlAction{{kind: ddlModifyColumn, name: "value", typeName: "BIGINT"}})
	if err != nil {
		t.Fatalf("modify nullable column: %v", err)
	}
	if !equalRows(updated.Rows, [][]string{{storedSQLNullValue}}) {
		t.Fatalf("null value changed: %#v", updated.Rows)
	}
}

func TestParseAlterTableActions(t *testing.T) {
	actions, err := parseAlterTableActions("ADD COLUMN active BOOLEAN, RENAME COLUMN name TO display_name, DROP COLUMN old_name")
	if err != nil {
		t.Fatalf("parse alter actions: %v", err)
	}
	if len(actions) != 3 || actions[0].kind != ddlAddColumn || actions[1].kind != ddlRenameColumn || actions[2].kind != ddlDropColumn {
		t.Fatalf("actions = %#v", actions)
	}
}

func TestAlterTableRenamePreservesTableDefinition(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter string
	}{
		{name: "without_to", alter: "ALTER TABLE users RENAME accounts"},
		{name: "with_to", alter: "ALTER TABLE users RENAME TO accounts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range []string{
				"CREATE TABLE users (id INT PRIMARY KEY, value INT NOT NULL)",
				"CREATE INDEX users_value_idx ON users (value)",
				"INSERT INTO users VALUES (1, 100)",
			} {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("execute %q: %v", query, err)
				}
			}

			if _, err := executeStatement(executor, test.alter); err != nil {
				t.Fatalf("rename table: %v", err)
			}
			result, err := executeStatement(executor, "SELECT id, value FROM accounts")
			if err != nil || !equalRows(result.rows, [][]string{{"1", "100"}}) {
				t.Fatalf("renamed rows = %#v, err = %v", result.rows, err)
			}
			if _, err := executeStatement(executor, "SELECT * FROM users"); !isFailureCode(err, 1146) {
				t.Fatalf("old table lookup error = %v, want code 1146", err)
			}

			table, found := executor.server.config.Catalog.Snapshot().Namespaces["app"].Tables["accounts"]
			if !found {
				t.Fatal("renamed table is missing from the catalog")
			}
			if table.Name != "accounts" {
				t.Fatalf("table name = %q, want accounts", table.Name)
			}
			if !equalStrings(table.Columns, []string{"id", "value"}) || !equalStrings(table.ColumnTypes, []string{"INT", "INT"}) {
				t.Fatalf("table columns = %#v types = %#v", table.Columns, table.ColumnTypes)
			}
			if !equalRows(table.Rows, [][]string{{"1", "100"}}) || len(table.Constraints) != 1 || len(table.Indexes) != 1 {
				t.Fatalf("renamed table definition = %#v", table)
			}
			if table.Constraints[0].Name != "PRIMARY" || !equalStrings(table.Constraints[0].Columns, []string{"id"}) {
				t.Fatalf("renamed table constraint = %#v", table.Constraints[0])
			}
			if table.Indexes[0].Name != "users_value_idx" || len(table.Indexes[0].Parts) != 1 || table.Indexes[0].Parts[0].Column != "value" {
				t.Fatalf("renamed table index = %#v", table.Indexes[0])
			}
		})
	}
}

func TestForeignKeyReferenceAllowsNoSpaceBeforeColumnList(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE parents (id INT PRIMARY KEY)"); err != nil {
		t.Fatalf("create parents: %v", err)
	}
	if _, err := executeStatement(executor, "CREATE TABLE children (id INT PRIMARY KEY, parent_id INT, FOREIGN KEY (parent_id) REFERENCES parents(id))"); err != nil {
		t.Fatalf("create children: %v", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO children VALUES (1, 99)"); !isFailureCode(err, 1452) {
		t.Fatalf("orphan insert = %v", err)
	}
}

func TestDropTableReferencedByForeignKeyFails(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE parent (id INT PRIMARY KEY)",
		"CREATE TABLE child (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_child_parent FOREIGN KEY (parent_id) REFERENCES parent(id))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup query %q: %v", query, err)
		}
	}
	_, err := executeStatement(executor, "DROP TABLE parent")
	if err == nil {
		t.Fatal("expected error dropping parent table referenced by foreign key, got nil")
	}
	failure, ok := err.(sqlFailure)
	if !ok || failure.code != 3730 {
		t.Fatalf("expected error code 3730, got: %v", err)
	}
}

func TestTruncateTableReferencedByForeignKeyFails(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE parent (id INT PRIMARY KEY)",
		"CREATE TABLE child (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_child_parent FOREIGN KEY (parent_id) REFERENCES parent(id))",
		"INSERT INTO parent VALUES (1)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup query %q: %v", query, err)
		}
	}

	// Case 1: Child table is empty
	_, err := executeStatement(executor, "TRUNCATE TABLE parent")
	if err == nil {
		t.Fatal("expected error truncating parent table when child is empty, got nil")
	}
	failure, ok := err.(sqlFailure)
	if !ok || failure.code != 1701 {
		t.Fatalf("expected error code 1701, got: %v", err)
	}
	expectedMessage := "Cannot truncate a table referenced in a foreign key constraint ('child', CONSTRAINT 'fk_child_parent')"
	if failure.message != expectedMessage {
		t.Fatalf("expected message %q, got %q", expectedMessage, failure.message)
	}

	// Case 2: Child table contains a row with NULL foreign key
	if _, err := executeStatement(executor, "INSERT INTO child VALUES (10, NULL)"); err != nil {
		t.Fatalf("insert null child: %v", err)
	}
	_, err = executeStatement(executor, "TRUNCATE TABLE parent")
	if err == nil {
		t.Fatal("expected error truncating parent table when child has null foreign key, got nil")
	}
	failure, ok = err.(sqlFailure)
	if !ok || failure.code != 1701 {
		t.Fatalf("expected error code 1701, got: %v", err)
	}

	// Case 3: Child table contains matching non-null rows
	if _, err := executeStatement(executor, "INSERT INTO child VALUES (1, 1)"); err != nil {
		t.Fatalf("insert matching child: %v", err)
	}
	_, err = executeStatement(executor, "TRUNCATE TABLE parent")
	if err == nil {
		t.Fatal("expected error truncating parent table when child has matching row, got nil")
	}
	failure, ok = err.(sqlFailure)
	if !ok || failure.code != 1701 {
		t.Fatalf("expected error code 1701, got: %v", err)
	}

	// Case 4: Non-referenced table (child) succeeds
	if _, err := executeStatement(executor, "TRUNCATE TABLE child"); err != nil {
		t.Fatalf("expected truncate of child table to succeed, got: %v", err)
	}

	// Case 5: Self-referencing table fails truncate
	for _, query := range []string{
		"CREATE TABLE self_ref (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_self FOREIGN KEY (parent_id) REFERENCES self_ref(id))",
		"INSERT INTO self_ref VALUES (1, NULL)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup self_ref %q: %v", query, err)
		}
	}
	_, err = executeStatement(executor, "TRUNCATE TABLE self_ref")
	if err == nil {
		t.Fatal("expected error truncating self-referencing table, got nil")
	}
	failure, ok = err.(sqlFailure)
	if !ok || failure.code != 1701 {
		t.Fatalf("expected error code 1701 for self_ref, got: %v", err)
	}
	expectedSelfMessage := "Cannot truncate a table referenced in a foreign key constraint ('self_ref', CONSTRAINT 'fk_self')"
	if failure.message != expectedSelfMessage {
		t.Fatalf("expected message %q, got %q", expectedSelfMessage, failure.message)
	}

	// Case 6: Standalone table truncate succeeds and clears rows
	if _, err := executeStatement(executor, "CREATE TABLE standalone (id INT PRIMARY KEY)"); err != nil {
		t.Fatalf("create standalone: %v", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO standalone VALUES (1)"); err != nil {
		t.Fatalf("insert standalone: %v", err)
	}
	if _, err := executeStatement(executor, "TRUNCATE TABLE standalone"); err != nil {
		t.Fatalf("truncate standalone: %v", err)
	}
	rows, err := executeStatement(executor, "SELECT * FROM standalone")
	if err != nil {
		t.Fatalf("select standalone: %v", err)
	}
	if len(rows.rows) != 0 {
		t.Fatalf("expected 0 rows after truncate, got %d", len(rows.rows))
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalRows(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !equalStrings(left[index], right[index]) {
			return false
		}
	}
	return true
}
