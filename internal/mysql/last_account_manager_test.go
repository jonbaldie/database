package mysql

import (
	"errors"
	"testing"
)

func TestAccountStatementsProtectLastManagerWithSQLIdentity(t *testing.T) {
	executor := managerExecutor(t)
	for _, statement := range []string{
		"ALTER USER 'admin' ACCOUNT LOCK",
		"DROP USER 'admin'",
		"REVOKE ACCOUNT_MANAGER ON *.* FROM 'admin'",
	} {
		_, err := executeStatement(executor, statement)
		var failure sqlFailure
		if !errors.As(err, &failure) || failure.code != 1396 || failure.state != "HY000" {
			t.Fatalf("%s: %v, want 1396/HY000", statement, err)
		}
	}
	seedSQL(t, executor,
		"CREATE USER 'other' IDENTIFIED BY 'other-manager-secret'",
		"GRANT ACCOUNT_MANAGER ON *.* TO 'other'",
		"ALTER USER 'other' ACCOUNT LOCK",
		"ALTER USER 'other' ACCOUNT LOCK",
		"DROP USER 'other'",
	)
}

func TestAccountStatementsAcceptEveryServerGrant(t *testing.T) {
	executor := managerExecutor(t)
	seedSQL(t, executor, "CREATE USER 'reader' IDENTIFIED BY 'reader-user-secret'")
	for _, privilege := range []string{"ACCOUNT_MANAGER", "NAMESPACE_MANAGER", "OPERATIONAL_OBSERVATION", "OPERATIONAL_CONTROL"} {
		seedSQL(t, executor, "GRANT "+privilege+" ON *.* TO 'reader'", "REVOKE "+privilege+" ON *.* FROM 'reader'")
	}
	executor.session.username = "reader"
	if _, err := executeStatement(executor, "CREATE USER 'denied' IDENTIFIED BY 'denied-user-secret'"); !isFailureCode(err, 1227) {
		t.Fatalf("unprivileged account created an account: %v", err)
	}
}

func managerExecutor(t *testing.T) *textStatementExecutor {
	t.Helper()
	executor := newSeededExecutor(t,
		"CREATE USER 'admin' IDENTIFIED BY 'admin-manager-secret'",
		"GRANT ACCOUNT_MANAGER ON *.* TO 'admin'",
	)
	executor.session.username = "admin"
	return executor
}
