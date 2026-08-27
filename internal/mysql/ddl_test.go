package mysql

import (
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
