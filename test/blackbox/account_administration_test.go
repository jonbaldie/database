package blackbox_test

import (
	"path/filepath"
	"reflect"
	"testing"

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
