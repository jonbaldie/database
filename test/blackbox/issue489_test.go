package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue489SchemaChangeFailuresUseMySQL84ErrorIdentities(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE issue489",
		"USE issue489",
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE nx (id INT PRIMARY KEY, v INT, INDEX (v))",
		"CREATE TABLE inv (id INT PRIMARY KEY, v INT, INDEX iv (v) INVISIBLE)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	columns := "c1 INT, c2 INT, c3 INT, c4 INT, c5 INT, c6 INT, c7 INT, c8 INT, c9 INT, c10 INT, c11 INT, c12 INT, c13 INT, c14 INT, c15 INT, c16 INT, c17 INT"
	parts := "c1, c2, c3, c4, c5, c6, c7, c8, c9, c10, c11, c12, c13, c14, c15, c16, c17"
	cases := []struct {
		query string
		code  uint16
		state string
	}{
		{"ALTER TABLE p DROP INDEX nosuch", 1091, "42000"},
		{"DROP INDEX nosuch ON p", 1091, "42000"},
		{"ALTER TABLE p DROP COLUMN nosuch", 1091, "42000"},
		{"ALTER TABLE p ALTER INDEX nosuch INVISIBLE", 1176, "42000"},
		{"CREATE TABLE k17 (" + columns + ", INDEX (" + parts + "))", 1070, "42000"},
		{"CREATE TABLE dc (a INT, CONSTRAINT ck CHECK (a > 0), CONSTRAINT ck CHECK (a < 9))", 3822, "HY000"},
		{"CREATE TABLE di (a INT, INDEX ix (a), INDEX ix (a))", 1061, "42000"},
		{"CREATE TABLE df (a INT, CONSTRAINT f FOREIGN KEY (a) REFERENCES p (id), CONSTRAINT f FOREIGN KEY (a) REFERENCES p (id))", 1826, "HY000"},
		{"CREATE TABLE f1 (a INT, FOREIGN KEY (a) REFERENCES nx (v))", 6125, "HY000"},
		{"CREATE TABLE f2 (a INT, FOREIGN KEY (a) REFERENCES nosuch (id))", 1824, "HY000"},
		{"CREATE TABLE f3 (a INT, FOREIGN KEY (a) REFERENCES p (nosuch))", 3734, "HY000"},
		{"CREATE TABLE ck1 (a INT, CHECK (nosuch > 0))", 3820, "HY000"},
		{"SELECT * FROM inv USE INDEX (iv)", 1176, "42000"},
	}
	for _, testCase := range cases {
		result := client.query(testCase.query)
		if result.err == "" || result.errCode != testCase.code || result.errState != testCase.state {
			t.Errorf("query %q returned error %q, code %d, SQLSTATE %q; want code %d, SQLSTATE %q", testCase.query, result.err, result.errCode, result.errState, testCase.code, testCase.state)
		}
	}
}
