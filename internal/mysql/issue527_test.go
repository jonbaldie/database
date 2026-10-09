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
		{"user-named", []string{"CREATE TABLE p (id INT PRIMARY KEY, code INT, CONSTRAINT uq_code UNIQUE (code))"}, "uq_code"},
		{"inline column UNIQUE", []string{"CREATE TABLE p (id INT PRIMARY KEY, code INT UNIQUE)"}, "p_code_unique"},
		{"added constraint", []string{"CREATE TABLE p (id INT PRIMARY KEY, code INT)", "ALTER TABLE p ADD CONSTRAINT uq_code UNIQUE (code)"}, "uq_code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range append(tc.setup, "INSERT INTO p VALUES (1, 10)") {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("setup %q: %v", query, err)
				}
			}
			for _, step := range []struct{ visibility, want string }{{"INVISIBLE", "NO"}, {"VISIBLE", "YES"}} {
				query := "ALTER TABLE p ALTER INDEX " + tc.index + " " + step.visibility
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("%q: %v", query, err)
				}
				result, err := executeStatement(executor, "SHOW INDEX FROM p WHERE Key_name = '"+tc.index+"'")
				if err != nil {
					t.Fatalf("SHOW INDEX: %v", err)
				}
				if len(result.rows) != 1 || result.rows[0][13] != step.want {
					t.Fatalf("SHOW INDEX after %q = %v, want Visible %s", query, result.rows, step.want)
				}
				if _, err := executeStatement(executor, "INSERT INTO p VALUES (2, 10)"); !isFailureCode(err, 1062) {
					t.Fatalf("duplicate code after %q: expected 1062, got %v", query, err)
				}
			}
		})
	}
}

// TestIssue527InvisibleUniqueKeyRoundTripsAndRejectsHints checks that SHOW
// CREATE TABLE keeps the visibility of a unique key, and that an index hint
// cannot name an invisible unique key.
func TestIssue527InvisibleUniqueKeyRoundTripsAndRejectsHints(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, CONSTRAINT uq_code UNIQUE (code))",
		"ALTER TABLE p ALTER INDEX uq_code INVISIBLE",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "SELECT id FROM p USE INDEX (uq_code) WHERE code = 1"); !isFailureCode(err, 1176) {
		t.Fatalf("hint on invisible uq_code: expected 1176, got %v", err)
	}
	result, err := executeStatement(executor, "SHOW CREATE TABLE p")
	if err != nil {
		t.Fatalf("SHOW CREATE TABLE: %v", err)
	}
	definition := result.rows[0][1]
	if !strings.Contains(definition, "CONSTRAINT `uq_code` UNIQUE (`code`) INVISIBLE") {
		t.Fatalf("SHOW CREATE TABLE = %s, want invisible uq_code", definition)
	}
	for _, query := range []string{"DROP TABLE p", definition} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%q: %v", query, err)
		}
	}
	result, err = executeStatement(executor, "SHOW INDEX FROM p WHERE Key_name = 'uq_code'")
	if err != nil {
		t.Fatalf("SHOW INDEX: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][13] != "NO" {
		t.Fatalf("SHOW INDEX after re-create = %v, want Visible NO", result.rows)
	}
}
