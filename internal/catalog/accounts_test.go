package catalog

import (
	"reflect"
	"testing"
)

func TestMigrateInitialAdministratorOnlyWhenCatalogIsEmpty(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}

	migrated, err := store.MigrateInitialAdministrator("admin", "legacy-hash")
	if err != nil || !migrated {
		t.Fatalf("migrate empty catalog: migrated=%t err=%v", migrated, err)
	}
	admin, found := store.Account("admin")
	wantGrants := []Grant{
		{Privilege: "ACCOUNT_MANAGER"},
		{Privilege: "NAMESPACE_MANAGER"},
		{Privilege: "OPERATIONAL_OBSERVATION"},
		{Privilege: "OPERATIONAL_CONTROL"},
	}
	if !found || admin.PasswordHash != "legacy-hash" || !reflect.DeepEqual(admin.Grants, wantGrants) {
		t.Fatalf("migrated administrator = %#v, found=%t", admin, found)
	}

	if err := store.DeleteAccount("admin"); err != nil {
		t.Fatalf("delete migrated administrator: %v", err)
	}
	if err := store.CreateAccount(Account{Name: "ops", PasswordHash: "ops-hash"}); err != nil {
		t.Fatalf("create operator account: %v", err)
	}
	migrated, err = store.MigrateInitialAdministrator("admin", "legacy-hash")
	if err != nil || migrated {
		t.Fatalf("migrate catalog with accounts: migrated=%t err=%v", migrated, err)
	}
	if _, found := store.Account("admin"); found {
		t.Fatal("migration restored a missing administrator beside an existing account")
	}
}
