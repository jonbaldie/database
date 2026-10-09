package mysql

import (
	"errors"
	"strings"
	"testing"
)

// TestIssue489SchemaChangeFailuresUseMySQLErrorNumbers checks that
// schema-change failures return the MySQL 8.4 error number and SQLSTATE.
func TestIssue489SchemaChangeFailuresUseMySQLErrorNumbers(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pid INT, CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES p (id))",
		"CREATE INDEX ic ON c (pid)",
		"CREATE TABLE nx (id INT PRIMARY KEY, v INT, INDEX (v))",
		"CREATE TABLE inv (id INT PRIMARY KEY, v INT, INDEX iv (v) INVISIBLE)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	columns := make([]string, 17)
	for i := range columns {
		columns[i] = "c" + string(rune('a'+i))
	}
	for _, tc := range []struct {
		query string
		code  uint16
		state string
	}{
		{"ALTER TABLE p DROP INDEX nosuch", 1091, "42000"},
		{"DROP INDEX nosuch ON p", 1091, "42000"},
		{"ALTER TABLE p DROP COLUMN nosuch", 1091, "42000"},
		{"ALTER TABLE p ALTER INDEX nosuch INVISIBLE", 1176, "42000"},
		{"CREATE TABLE k17 (" + strings.Join(columns, " INT, ") + " INT, INDEX (" + strings.Join(columns, ", ") + "))", 1070, "42000"},
		{"CREATE TABLE dc (a INT, CONSTRAINT ck CHECK (a > 0), CONSTRAINT ck CHECK (a < 9))", 3822, "HY000"},
		{"CREATE TABLE df (a INT, CONSTRAINT f FOREIGN KEY (a) REFERENCES p (id), CONSTRAINT f FOREIGN KEY (a) REFERENCES p (id))", 1826, "HY000"},
		{"CREATE TABLE f1 (a INT, FOREIGN KEY (a) REFERENCES nx (v))", 6125, "HY000"},
		{"CREATE TABLE f2 (a INT, FOREIGN KEY (a) REFERENCES nosuch (id))", 1824, "HY000"},
		{"CREATE TABLE f3 (a INT, FOREIGN KEY (a) REFERENCES p (nosuch))", 3734, "HY000"},
		{"CREATE TABLE ck1 (a INT, CHECK (nosuch > 0))", 3820, "HY000"},
		{"SELECT * FROM inv USE INDEX (iv)", 1176, "42000"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			_, err := executeStatement(executor, tc.query)
			var failure sqlFailure
			if !errors.As(err, &failure) {
				t.Fatalf("error = %v, want SQL failure %d", err, tc.code)
			}
			if failure.code != tc.code || failure.state != tc.state {
				t.Fatalf("error = %d (%s) %q, want %d (%s)", failure.code, failure.state, failure.message, tc.code, tc.state)
			}
		})
	}
}
