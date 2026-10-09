package mysql

import (
	"strings"
	"testing"
)

// TestIssue527AlterIndexChangesUniqueKeyVisibility checks that ALTER INDEX
// changes the visibility of a unique key that a table definition declares, by
// the name SHOW INDEX lists.
func TestIssue527AlterIndexChangesUniqueKeyVisibility(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup []string
		index string
	}{
		{"user-named table constraint", []string{"CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64), CONSTRAINT uq_email UNIQUE (email))"}, "uq_email"},
		{"server-named inline column UNIQUE", []string{"CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64) UNIQUE)"}, "accounts_email_unique"},
		{"added constraint", []string{"CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64))", "ALTER TABLE accounts ADD CONSTRAINT uq_email UNIQUE (email)"}, "uq_email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range append(tc.setup, "INSERT INTO accounts VALUES (1, 'a@example.com')") {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("setup %q: %v", query, err)
				}
			}
			assertIssue527Visibility(t, executor, tc.index, "YES")
			alter := "ALTER TABLE accounts ALTER INDEX " + strings.ToUpper(tc.index) + " INVISIBLE"
			if _, err := executeStatement(executor, alter); err != nil {
				t.Fatalf("%q: %v", alter, err)
			}
			assertIssue527Visibility(t, executor, tc.index, "NO")
			if _, err := executeStatement(executor, "INSERT INTO accounts VALUES (2, 'a@example.com')"); !isFailureCode(err, 1062) {
				t.Fatalf("duplicate email with invisible %s: expected 1062, got %v", tc.index, err)
			}
			if _, err := executeStatement(executor, "SELECT id FROM accounts USE INDEX ("+tc.index+")"); !isFailureCode(err, 3522) {
				t.Fatalf("hint on invisible %s: expected 3522, got %v", tc.index, err)
			}
			if _, err := executeStatement(executor, "ALTER TABLE accounts ALTER INDEX "+tc.index+" VISIBLE"); err != nil {
				t.Fatalf("ALTER INDEX VISIBLE: %v", err)
			}
			assertIssue527Visibility(t, executor, tc.index, "YES")
		})
	}
}

// TestIssue527InvisibleUniqueKeySurvivesShowCreateTable checks that SHOW
// CREATE TABLE keeps an invisible unique key invisible when it is replayed.
func TestIssue527InvisibleUniqueKeySurvivesShowCreateTable(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64), CONSTRAINT uq_email UNIQUE (email))",
		"ALTER TABLE accounts ALTER INDEX uq_email INVISIBLE",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SHOW CREATE TABLE accounts")
	if err != nil {
		t.Fatalf("SHOW CREATE TABLE: %v", err)
	}
	definition := result.rows[0][1]
	for _, query := range []string{"DROP TABLE accounts", definition} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("replay %q: %v", query, err)
		}
	}
	assertIssue527Visibility(t, executor, "uq_email", "NO")
}

// TestIssue527PrimaryKeyStaysVisible checks that ALTER INDEX still rejects an
// invisible primary key.
func TestIssue527PrimaryKeyStaysVisible(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64))"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := executeStatement(executor, "ALTER TABLE accounts ALTER INDEX `PRIMARY` INVISIBLE"); !isFailureCode(err, 3522) {
		t.Fatalf("invisible primary key: expected 3522, got %v", err)
	}
	assertIssue527Visibility(t, executor, "PRIMARY", "YES")
}

func assertIssue527Visibility(t *testing.T, executor *textStatementExecutor, index, visible string) {
	t.Helper()
	result, err := executeStatement(executor, "SHOW INDEX FROM accounts WHERE Key_name = '"+index+"'")
	if err != nil {
		t.Fatalf("SHOW INDEX: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][13] != visible {
		t.Fatalf("SHOW INDEX rows for %s = %v, want one row with Visible %s", index, result.rows, visible)
	}
}
