package blackbox_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLAccountAdministrationPersistsAcrossRestart(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "account-admin-secret")

	process, address := startMySQLServer(t, runner, directory)
	admin := newWireClient(t, address, "admin", "account-admin-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE DATABASE application")
	mustQuery(t, admin, "USE application")
	mustQuery(t, admin, "CREATE TABLE records (id INT)")
	mustQuery(t, admin, "INSERT INTO records VALUES (1)")
	mustQuery(t, admin, "CREATE USER 'reader' IDENTIFIED BY 'reader-password'")
	mustQuery(t, admin, "GRANT DATA_READ ON application.* TO 'reader'")
	reader := newWireClient(t, address, "reader", "reader-password")
	mustQuery(t, reader, "USE application")
	if result := reader.query("SELECT id FROM records"); result.err != "" || len(result.rows) != 1 {
		t.Fatalf("granted read: %#v", result)
	}
	mustQuery(t, admin, "REVOKE DATA_READ ON application.* FROM 'reader'")
	if result := reader.query("SELECT id FROM records"); result.err == "" {
		t.Fatalf("revoked read still succeeded: %#v", result)
	}
	_ = reader.close()
	mustQuery(t, admin, "GRANT DATA_READ ON application.* TO 'reader'")
	mustQuery(t, admin, "ALTER USER 'reader' IDENTIFIED BY 'changed-reader-password'")
	if result := admin.query("REVOKE ACCOUNT_MANAGER ON *.* FROM 'admin'"); result.err == "" {
		t.Fatalf("last account manager was removed: %#v", result)
	}
	_ = admin.close()
	_ = process.Stop()
	_ = process.Wait()

	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	reader = newWireClient(t, address, "reader", "changed-reader-password")
	defer reader.close()
	if result := reader.query("SELECT 1"); result.err != "" {
		t.Fatalf("durable account login query: %#v", result)
	}
}

func TestDroppedInitialAdministratorStaysDroppedAcrossRestart(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "account-admin-secret")

	process, address := startMySQLServer(t, runner, directory)
	admin := newWireClient(t, address, "admin", "account-admin-secret")
	mustQuery(t, admin, "CREATE USER 'ops' IDENTIFIED BY 'ops-account-secret'")
	mustQuery(t, admin, "GRANT ACCOUNT_MANAGER ON *.* TO 'ops'")
	mustQuery(t, admin, "DROP USER 'admin'")
	_ = admin.close()
	ops := newWireClient(t, address, "ops", "ops-account-secret")
	accounts := ops.query("SELECT * FROM information_schema.ACCOUNTS")
	if accounts.err != "" {
		t.Fatalf("read account catalog before restart: %#v", accounts)
	}
	for _, account := range accounts.rows {
		if account[0] == "admin" {
			t.Fatalf("DROP USER did not remove the initial administrator: %#v", accounts.rows)
		}
	}
	_ = ops.close()
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("first shutdown: %#v", result)
	}
	addLegacyPasswordHash(t, directory)

	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	ops = newWireClient(t, address, "ops", "ops-account-secret")
	defer ops.close()
	accounts = ops.query("SELECT * FROM information_schema.ACCOUNTS")
	if accounts.err != "" {
		t.Fatalf("read account catalog after restart: %#v", accounts)
	}
	for _, account := range accounts.rows {
		if account[0] == "admin" {
			t.Fatalf("dropped initial administrator returned after restart: %#v", accounts.rows)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), `"password_hash"`) {
		t.Fatalf("legacy administrator verifier remains in instance metadata: %s", metadata)
	}
}

func addLegacyPasswordHash(t *testing.T, directory string) {
	t.Helper()
	path := filepath.Join(directory, "instance.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(contents, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata["password_hash"], err = json.Marshal("legacy-bootstrap-hash")
	if err != nil {
		t.Fatal(err)
	}
	contents, err = json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInitialAdministratorPasswordChangeSurvivesRestartAndBackup(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "account-admin-secret")

	process, address := startMySQLServer(t, runner, directory)
	admin := newWireClient(t, address, "admin", "account-admin-secret")
	mustQuery(t, admin, "ALTER USER 'admin' IDENTIFIED BY 'updated-admin-secret'")
	backup := admin.query("BACKUP INSTANCE")
	if backup.err != "" {
		t.Fatalf("backup instance: %#v", backup)
	}
	files := make(map[string]string, len(backup.rows))
	for _, row := range backup.rows {
		files[row[0]] = row[1]
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(files["instance.json"]), &metadata); err != nil {
		t.Fatalf("decode backup instance metadata: %v", err)
	}
	if _, found := metadata["password_hash"]; found {
		t.Fatalf("backup instance metadata contains a second password verifier: %s", files["instance.json"])
	}
	var backupCatalog struct {
		Accounts map[string]struct {
			PasswordHash string `json:"password_hash"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(files["catalog.json"]), &backupCatalog); err != nil {
		t.Fatalf("decode backup catalog: %v", err)
	}
	currentCatalog, err := os.ReadFile(filepath.Join(directory, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var currentAccounts struct {
		Accounts map[string]struct {
			PasswordHash string `json:"password_hash"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(currentCatalog, &currentAccounts); err != nil {
		t.Fatalf("decode current catalog: %v", err)
	}
	backupAdmin, backupFound := backupCatalog.Accounts["admin"]
	currentAdmin, currentFound := currentAccounts.Accounts["admin"]
	if !backupFound || !currentFound || backupAdmin.PasswordHash == "" || backupAdmin.PasswordHash != currentAdmin.PasswordHash {
		t.Fatalf("backup administrator does not match catalog state: backup=%#v current=%#v", backupAdmin, currentAdmin)
	}
	_ = admin.close()
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("first shutdown: %#v", result)
	}

	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	admin = newWireClient(t, address, "admin", "updated-admin-secret")
	defer admin.close()
	mustQuery(t, admin, "SELECT 1")
	oldCredentials, err := sql.Open("mysql", "admin:account-admin-secret@tcp("+address+")/")
	if err != nil {
		t.Fatalf("open old credentials: %v", err)
	}
	defer oldCredentials.Close()
	if err := oldCredentials.Ping(); err == nil {
		t.Fatal("old administrator password still works after restart")
	}
}

func TestMySQLCatalogMetadataFollowsNamespaceGrants(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "catalog-grants-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "catalog-grants-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE DATABASE public_data")
	mustQuery(t, admin, "CREATE DATABASE private_data")
	mustQuery(t, admin, "CREATE USER 'cataloguser' IDENTIFIED BY 'catalog-user-secret'")
	mustQuery(t, admin, "GRANT DATA_READ ON public_data.* TO 'cataloguser'")
	user := newWireClient(t, address, "cataloguser", "catalog-user-secret")
	defer user.close()

	if result := user.query("SHOW DATABASES"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"information_schema"}, {"public_data"}}) {
		t.Fatalf("visible databases: %#v", result)
	}
	result := user.query("SELECT SCHEMA_NAME FROM information_schema.schemata")
	if result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"information_schema"}, {"public_data"}}) {
		t.Fatalf("visible schemata: %#v", result)
	}
	if result := user.query("SHOW CREATE DATABASE private_data"); result.errCode != 1044 {
		t.Fatalf("hidden namespace result: %#v", result)
	}
}

func TestMySQLCrossDatabaseGrantAuthorizationViaWire(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "cross-db-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "cross-db-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE DATABASE db_public")
	mustQuery(t, admin, "CREATE DATABASE db_secret")
	mustQuery(t, admin, "CREATE TABLE db_secret.confidential (secret_data VARCHAR(100) PRIMARY KEY)")
	mustQuery(t, admin, "INSERT INTO db_secret.confidential VALUES ('nuclear_launch_codes')")
	mustQuery(t, admin, "CREATE USER 'bob' IDENTIFIED BY 'password12345'")
	mustQuery(t, admin, "GRANT DATA_READ ON db_public.* TO 'bob'")
	mustQuery(t, admin, "GRANT DATA_WRITE ON db_public.* TO 'bob'")
	mustQuery(t, admin, "GRANT SCHEMA_MANAGEMENT ON db_public.* TO 'bob'")

	bob := newWireClient(t, address, "bob", "password12345")
	defer bob.close()

	if res := bob.query("SELECT * FROM db_secret.confidential"); res.errCode != 1044 && res.errCode != 1142 {
		t.Fatalf("expected 1044 or 1142 on cross-db read without db, got %#v", res)
	}

	if res := bob.query("SELECT 1"); res.err != "" {
		t.Fatalf("expected select 1 to succeed without db, got %#v", res)
	}

	mustQuery(t, bob, "USE db_public")

	if res := bob.query("SELECT * FROM db_secret.confidential"); res.errCode != 1044 && res.errCode != 1142 {
		t.Fatalf("expected 1044 or 1142 on cross-db read with use db_public, got %#v", res)
	}
	if res := bob.query("INSERT INTO db_secret.confidential VALUES ('unauthorized_row')"); res.errCode != 1044 && res.errCode != 1142 {
		t.Fatalf("expected 1044 or 1142 on cross-db insert, got %#v", res)
	}
	if res := bob.query("CREATE TABLE db_secret.pwned (id INT)"); res.errCode != 1044 && res.errCode != 1142 {
		t.Fatalf("expected 1044 or 1142 on cross-db create table, got %#v", res)
	}
	if res := bob.query("TRUNCATE TABLE db_secret.confidential"); res.errCode != 1044 && res.errCode != 1142 {
		t.Fatalf("expected 1044 or 1142 on cross-db truncate table, got %#v", res)
	}
}

func TestMySQLAccountAdministrationAppliesTheInitCredentialPolicy(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "account-policy-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	admin := newWireClient(t, address, "admin", "account-policy-secret")
	defer admin.close()
	for _, statement := range []string{
		"CREATE USER 'aš' IDENTIFIED BY 'contract-valid-password'",
		"CREATE USER 'reader@localhost' IDENTIFIED BY 'contract-valid-password'",
		"CREATE USER '_reader' IDENTIFIED BY 'contract-valid-password'",
		"CREATE USER 'reader' IDENTIFIED BY 'eleven-byte'",
		"ALTER USER 'admin' IDENTIFIED BY 'eleven-byte'",
	} {
		if result := admin.query(statement); !strings.Contains(result.err, "invalid account or password") {
			t.Fatalf("%s: %#v, want invalid account or password", statement, result)
		}
	}
	mustQuery(t, admin, "CREATE USER 'Reader.ops_2-x' IDENTIFIED BY 'twelve-bytes'")
	reader := newWireClient(t, address, "Reader.ops_2-x", "twelve-bytes")
	defer reader.close()
	mustQuery(t, reader, "SELECT 1")
}
