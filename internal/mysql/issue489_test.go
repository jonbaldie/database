package mysql

import (
	"errors"
	"testing"
)

func TestSchemaChangeFailuresUseMySQLErrorNumbers(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE nx (id INT PRIMARY KEY, v INT, INDEX (v))",
		"CREATE TABLE inv (id INT PRIMARY KEY, v INT, INDEX iv (v) INVISIBLE)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, test := range []struct {
		query    string
		code     uint16
		sqlState string
	}{
		{"ALTER TABLE p DROP INDEX nosuch", 1091, "42000"},
		{"ALTER TABLE p DROP KEY nosuch", 1091, "42000"},
		{"DROP INDEX nosuch ON p", 1091, "42000"},
		{"ALTER TABLE p DROP COLUMN nosuch", 1091, "42000"},
		{"ALTER TABLE p ALTER INDEX nosuch INVISIBLE", 1176, "42000"},
		{"CREATE TABLE k17 (c1 INT, c2 INT, c3 INT, c4 INT, c5 INT, c6 INT, c7 INT, c8 INT, c9 INT, c10 INT, c11 INT, c12 INT, c13 INT, c14 INT, c15 INT, c16 INT, c17 INT, INDEX (c1, c2, c3, c4, c5, c6, c7, c8, c9, c10, c11, c12, c13, c14, c15, c16, c17))", 1070, "42000"},
		{"CREATE TABLE dc (a INT, CONSTRAINT ck CHECK (a > 0), CONSTRAINT ck CHECK (a < 9))", 3822, "HY000"},
		{"CREATE TABLE df (a INT, CONSTRAINT f FOREIGN KEY (a) REFERENCES p (id), CONSTRAINT f FOREIGN KEY (a) REFERENCES p (id))", 1826, "HY000"},
		{"CREATE TABLE f1 (a INT, FOREIGN KEY (a) REFERENCES nx (v))", 6125, "HY000"},
		{"CREATE TABLE f2 (a INT, FOREIGN KEY (a) REFERENCES nosuch (id))", 1824, "HY000"},
		{"CREATE TABLE f3 (a INT, FOREIGN KEY (a) REFERENCES p (nosuch))", 3734, "HY000"},
		{"CREATE TABLE ck1 (a INT, CHECK (nosuch > 0))", 3820, "HY000"},
		{"SELECT * FROM inv USE INDEX (iv)", 1176, "42000"},
	} {
		_, err := executeStatement(executor, test.query)
		var failure sqlFailure
		if !errors.As(err, &failure) || failure.code != test.code || failure.state != test.sqlState {
			t.Errorf("%s: error = %#v, want %d (%s)", test.query, err, test.code, test.sqlState)
		}
	}
}
