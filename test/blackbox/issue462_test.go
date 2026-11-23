package blackbox_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/credential"
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestDroppedInitialAdministratorStaysDropped(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "account-admin-secret")
	process, address := startMySQLServer(t, runner, directory)
	admin := newWireClient(t, address, "admin", "account-admin-secret")
	mustQuery(t, admin, "CREATE USER 'ops' IDENTIFIED BY 'operator-secret'")
	mustQuery(t, admin, "GRANT ACCOUNT_MANAGER ON *.* TO 'ops'")
	ops := newWireClient(t, address, "ops", "operator-secret")
	mustQuery(t, ops, "DROP USER 'admin'")
	_ = admin.close()
	_ = ops.close()
	_ = process.Stop()
	_ = process.Wait()
	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	db, err := sql.Open("mysql", "admin:account-admin-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err == nil {
		t.Fatal("dropped initial administrator can log in after restart")
	}
}

func TestLegacyBootstrapRunsOnceAndPreservesAccounts(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_accounts_%t", existing), func(t *testing.T) {
			runner := blackbox.Runner{Executable: executable}
			directory := filepath.Join(t.TempDir(), "instance")
			initializeServer(t, runner, directory, "account-admin-secret")
			metadataPath := filepath.Join(directory, "instance.json")
			data, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatal(err)
			}
			var metadata map[string]any
			if err := json.Unmarshal(data, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata["password_hash"] = credential.PasswordHash("account-admin-secret")
			data, err = json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(metadataPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			definition := catalog.Definition{Namespaces: map[string]catalog.Namespace{}}
			if existing {
				definition.Accounts = map[string]catalog.Account{"ops": catalog.InitialAccount("ops", credential.PasswordHash("operator-secret"))}
			}
			data, err = json.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "catalog.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			process, address := startMySQLServer(t, runner, directory)
			name, password := "admin", "account-admin-secret"
			if existing {
				name, password = "ops", "operator-secret"
			}
			client := newWireClient(t, address, name, password)
			mustQuery(t, client, "SELECT 1")
			_ = client.close()
			_ = process.Stop()
			_ = process.Wait()
			data, err = os.ReadFile(metadataPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "password_hash") {
				t.Fatal("legacy verifier remains in metadata")
			}
			process, address = startMySQLServer(t, runner, directory)
			defer func() { _ = process.Stop(); _ = process.Wait() }()
			client = newWireClient(t, address, name, password)
			defer client.close()
			mustQuery(t, client, "SELECT 1")
			if existing {
				db, err := sql.Open("mysql", "admin:account-admin-secret@tcp("+address+")/")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := db.Ping(); err == nil {
					t.Fatal("legacy administrator recreated despite existing accounts")
				}
			}
		})
	}
}

func TestInitialAdministratorPasswordChangeSurvivesRestart(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "account-admin-secret")
	process, address := startMySQLServer(t, runner, directory)
	admin := newWireClient(t, address, "admin", "account-admin-secret")
	mustQuery(t, admin, "ALTER USER 'admin' IDENTIFIED BY 'changed-admin-secret'")
	_ = admin.close()
	_ = process.Stop()
	_ = process.Wait()
	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	admin = newWireClient(t, address, "admin", "changed-admin-secret")
	defer admin.close()
	mustQuery(t, admin, "SELECT 1")
	db, err := sql.Open("mysql", "admin:account-admin-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err == nil {
		t.Fatal("old administrator password works after restart")
	}
}

func TestInitStoresAdministratorOnlyInCatalog(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "account-admin-secret")
	data, err := os.ReadFile(filepath.Join(directory, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var definition catalog.Definition
	if err := json.Unmarshal(data, &definition); err != nil {
		t.Fatal(err)
	}
	admin, ok := definition.Accounts["admin"]
	if !definition.BootstrapComplete || !ok || admin.PasswordHash != credential.PasswordHash("account-admin-secret") || len(admin.Grants) != 4 {
		t.Fatalf("initial catalog did not contain the administrator and four grants")
	}
	data, err = os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "password_hash") {
		t.Fatal("initial verifier was stored in metadata")
	}
}
