package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIssue543ColumnPrivilegesFollowVisibleNamespaceGrants(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"CREATE TABLE p (id INT)", "CREATE DATABASE hidden", "CREATE TABLE hidden.p (id INT)"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, privilege, want string
	}{
		{"reader", "DATA_READ", "select"},
		{"writer", "DATA_WRITE", "insert,update"},
		{"designer", "SCHEMA_MANAGEMENT", "references"},
		{"manager", "NAMESPACE_MANAGER", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			grant := catalog.Grant{Privilege: test.privilege, Namespace: "app"}
			if test.privilege == "NAMESPACE_MANAGER" {
				grant.Namespace = ""
			}
			if err := executor.server.config.Catalog.CreateAccount(catalog.Account{Name: test.name, PasswordHash: "hash", Grants: []catalog.Grant{grant}}); err != nil {
				t.Fatal(err)
			}
			reader := &textStatementExecutor{session: &session{server: executor.server, username: test.name, timeZone: "UTC", initialTimeZone: "UTC"}}
			result, err := executeStatement(reader, "SELECT TABLE_SCHEMA, PRIVILEGES FROM information_schema.COLUMNS WHERE TABLE_SCHEMA <> 'information_schema'")
			want := [][]string{{"app", test.want}}
			if test.privilege == "NAMESPACE_MANAGER" {
				want = nil
			}
			if err != nil || !equalRows(result.rows, want) {
				t.Fatalf("visible columns = %#v, %v; want %#v", result, err, want)
			}
		})
	}
}

func TestIssue543TableFactsAndVirtualColumnNullability(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"CREATE TABLE p (id INT PRIMARY KEY AUTO_INCREMENT)", "INSERT INTO p VALUES (DEFAULT), (DEFAULT)"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatal(err)
		}
	}
	result, err := executeStatement(executor, "SELECT VERSION, ROW_FORMAT, AVG_ROW_LENGTH, DATA_LENGTH, MAX_DATA_LENGTH, INDEX_LENGTH, DATA_FREE, CREATE_TIME, UPDATE_TIME, CHECK_TIME, CHECKSUM FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app'")
	if err != nil || len(result.nulls) != 1 || !equalNulls(result.nulls, [][]bool{{true, true, true, true, true, true, true, true, true, true, true}}) {
		t.Fatalf("physical facts = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT TABLE_ROWS, AUTO_INCREMENT, CREATE_OPTIONS, TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app'")
	if err != nil || !equalRows(result.rows, [][]string{{"2", "3", "", ""}}) {
		t.Fatalf("table facts = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT TABLE_ROWS, AUTO_INCREMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'tables'")
	if err != nil || !equalNulls(result.nulls, [][]bool{{true, true}}) {
		t.Fatalf("virtual table facts = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT COLUMN_NAME, IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'tables' AND (COLUMN_NAME = 'TABLE_TYPE' OR COLUMN_NAME = 'TABLE_ROWS') ORDER BY ORDINAL_POSITION")
	if err != nil || !equalRows(result.rows, [][]string{{"TABLE_TYPE", "NO"}, {"TABLE_ROWS", "YES"}}) {
		t.Fatalf("virtual nullability = %#v, %v", result, err)
	}
	if _, err := executeStatement(executor, "TRUNCATE TABLE p"); err != nil {
		t.Fatal(err)
	}
	result, err = executeStatement(executor, "SELECT TABLE_ROWS, AUTO_INCREMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app'")
	if err != nil || !equalRows(result.rows, [][]string{{"0", "1"}}) {
		t.Fatalf("table facts after truncate = %#v, %v", result, err)
	}
}
