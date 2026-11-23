package catalog

import "testing"

func TestBootstrapLegacyDoesNotRecreateAccountAfterCompletion(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.BootstrapLegacy("admin", "old-hash"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount("admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.Rows().Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Rows().Close()
	if err := store.BootstrapLegacy("admin", "old-hash"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Account("admin"); ok {
		t.Fatal("account was recreated after bootstrap completed")
	}
}
