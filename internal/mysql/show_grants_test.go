package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestShowGrantsReportsCurrentAccountPrivileges(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if err := executor.server.config.Catalog.CreateAccount(catalog.Account{
		Name:         "admin",
		PasswordHash: "hash",
		Grants: []catalog.Grant{
			{Privilege: accountManagerPrivilege},
			{Privilege: "DATA_READ", Namespace: "app"},
		},
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	executor.session.username = "admin"
	result, err := executeStatement(executor, "SHOW GRANTS")
	if err != nil {
		t.Fatalf("SHOW GRANTS: %v", err)
	}
	if got := result.columns[0]; got != "Grants for admin" {
		t.Fatalf("column = %q", got)
	}
	want := [][]string{
		{"GRANT ACCOUNT_MANAGER ON *.* TO 'admin'"},
		{"GRANT DATA_READ ON app.* TO 'admin'"},
	}
	if !equalRows(result.rows, want) {
		t.Fatalf("rows = %#v", result.rows)
	}
	result, err = executeStatement(executor, "SHOW GRANTS FOR CURRENT_USER")
	if err != nil || !equalRows(result.rows, want) {
		t.Fatalf("SHOW GRANTS FOR CURRENT_USER = %#v, %v", result, err)
	}
}

func TestShowGrantsForOtherAccountRequiresManager(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, account := range []catalog.Account{
		{Name: "admin", PasswordHash: "hash", Grants: []catalog.Grant{{Privilege: accountManagerPrivilege}}},
		{Name: "reader", PasswordHash: "hash", Grants: []catalog.Grant{{Privilege: "DATA_READ", Namespace: "app"}}},
	} {
		if err := executor.server.config.Catalog.CreateAccount(account); err != nil {
			t.Fatalf("create %s: %v", account.Name, err)
		}
	}
	executor.session.username = "reader"
	if _, err := executeStatement(executor, "SHOW GRANTS FOR 'admin'"); !isFailureCode(err, 1227) {
		t.Fatalf("reader saw other grants: %v", err)
	}
	executor.session.username = "admin"
	result, err := executeStatement(executor, "SHOW GRANTS FOR 'reader'")
	if err != nil || !equalRows(result.rows, [][]string{{"GRANT DATA_READ ON app.* TO 'reader'"}}) {
		t.Fatalf("manager SHOW GRANTS FOR reader = %#v, %v", result, err)
	}
}
