package blackbox_test

import (
	"path/filepath"
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
