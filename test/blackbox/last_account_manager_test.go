package blackbox_test

import (
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLLastAccountManagerProtection(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "manager-rule-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	admin := newWireClient(t, address, "admin", "manager-rule-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE USER 'other' IDENTIFIED BY 'other-manager-secret'")
	mustQuery(t, admin, "GRANT ACCOUNT_MANAGER ON *.* TO 'other'")
	mustQuery(t, admin, "ALTER USER 'other' ACCOUNT LOCK")
	mustQuery(t, admin, "ALTER USER 'other' ACCOUNT LOCK")
	mustQuery(t, admin, "REVOKE ACCOUNT_MANAGER ON *.* FROM 'other'")
	mustQuery(t, admin, "DROP USER 'other'")
	for _, statement := range []string{
		"ALTER USER 'admin' ACCOUNT LOCK",
		"DROP USER 'admin'",
		"REVOKE ACCOUNT_MANAGER ON *.* FROM 'admin'",
	} {
		result := admin.query(statement)
		if result.errCode != 1396 {
			t.Fatalf("%s: %#v, want error 1396", statement, result)
		}
		mustQuery(t, admin, "SELECT 1")
	}
	_ = admin.close()
	_ = process.Stop()
	_ = process.Wait()
	process, address = startMySQLServer(t, runner, directory)
	admin = newWireClient(t, address, "admin", "manager-rule-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE USER 'after_restart' IDENTIFIED BY 'after-restart-secret'")
}
