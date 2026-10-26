package catalog_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestAccountWritesProtectLastManager(t *testing.T) {
	for _, operation := range []string{"lock", "drop", "revoke", "batch"} {
		t.Run(operation, func(t *testing.T) {
			directory := t.TempDir()
			store := managerStore(t, directory)
			before := store.Snapshot()
			var err error
			switch operation {
			case "lock":
				err = store.UpdateAccount("manager", func(account *catalog.Account) error { account.Locked = true; return nil })
			case "drop":
				err = store.DeleteAccount("manager")
			case "revoke":
				err = store.UpdateAccount("manager", func(account *catalog.Account) error { account.Grants = nil; return nil })
			case "batch":
				err = store.UpdateAccounts(func(accounts map[string]catalog.Account) error { clear(accounts); return nil })
			}
			if !errors.Is(err, catalog.ErrLastAccountManager) {
				t.Fatalf("%s last manager: %v", operation, err)
			}
			if !reflect.DeepEqual(before, store.Snapshot()) {
				t.Fatal("rejected change was published")
			}
			reopened, err := catalog.Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, reopened.Snapshot()) {
				t.Fatal("rejected change was persisted")
			}
		})
	}
}

func TestConcurrentAccountWritesKeepOneManager(t *testing.T) {
	for _, operation := range []string{"lock", "drop", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			directory := t.TempDir()
			store := managerStore(t, directory)
			if err := store.EnsureAccount("other", "hash", []catalog.Grant{{Privilege: catalog.AccountManagerPrivilege}}); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			var writers sync.WaitGroup
			for _, name := range []string{"manager", "other"} {
				writers.Go(func() {
					<-start
					if operation == "drop" {
						results <- store.DeleteAccount(name)
						return
					}
					results <- store.UpdateAccount(name, func(account *catalog.Account) error {
						if operation == "lock" {
							account.Locked = true
						} else {
							account.Grants = nil
						}
						return nil
					})
				})
			}
			close(start)
			writers.Wait()
			close(results)
			succeeded, protected := 0, 0
			for err := range results {
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, catalog.ErrLastAccountManager):
					protected++
				default:
					t.Fatal(err)
				}
			}
			if succeeded != 1 || protected != 1 {
				t.Fatalf("successes=%d protected=%d, want 1 each", succeeded, protected)
			}
			reopened, err := catalog.Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			managers := 0
			for _, account := range reopened.Snapshot().Accounts {
				if !account.Locked && account.HasGrant(catalog.AccountManagerPrivilege, "") {
					managers++
				}
			}
			if managers != 1 {
				t.Fatalf("durable enabled managers=%d, want 1", managers)
			}
		})
	}
}

func TestUpdateAccountsRollsBackCallbackFailure(t *testing.T) {
	store := managerStore(t, t.TempDir())
	before := store.Snapshot()
	failure := errors.New("reject staged change")
	err := store.UpdateAccounts(func(accounts map[string]catalog.Account) error {
		account := accounts["manager"]
		account.Grants[0].Privilege = "DATA_READ"
		accounts["manager"] = account
		return failure
	})
	if !errors.Is(err, failure) || !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatalf("callback failure published state: %v", err)
	}
}

func TestUpdateAccountsCanTransferManager(t *testing.T) {
	store := managerStore(t, t.TempDir())
	if err := store.UpdateAccounts(func(accounts map[string]catalog.Account) error {
		delete(accounts, "manager")
		accounts["replacement"] = catalog.Account{Name: "replacement", PasswordHash: "hash", Grants: []catalog.Grant{{Privilege: catalog.AccountManagerPrivilege}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, found := store.Account("manager"); found {
		t.Fatal("old manager remains")
	}
	if account, found := store.Account("replacement"); !found || account.Locked || !account.HasGrant(catalog.AccountManagerPrivilege, "") {
		t.Fatal("replacement manager missing")
	}
}

func managerStore(t *testing.T, directory string) *catalog.Store {
	t.Helper()
	store, err := catalog.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureAccount("manager", "hash", []catalog.Grant{{Privilege: catalog.AccountManagerPrivilege}}); err != nil {
		t.Fatal(err)
	}
	return store
}
