package mysql

import (
	"sort"
	"strings"
	"testing"
)

func TestUnnamedUniqueKeysUseMySQLColumnDerivedNames(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE n8 (id INT PRIMARY KEY, a INT, b INT, UNIQUE (a), UNIQUE (a, b))",
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE q (id INT PRIMARY KEY, a INT, b INT, INDEX a (b), UNIQUE (a), INDEX (a))",
		"CREATE TABLE r (id INT, `primary` INT, UNIQUE (`primary`))",
		"CREATE TABLE s (id INT PRIMARY KEY, a INT, b INT, UNIQUE (a))",
		"ALTER TABLE s ADD UNIQUE (a, b)",
		"ALTER TABLE s ADD UNIQUE (b)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for table, want := range map[string]string{
		"n8": "PRIMARY(id),a(a),a_2(a,b)",
		"p":  "PRIMARY(id),code(code)",
		"q":  "PRIMARY(id),a(b),a_2(a),a_3(a)",
		"r":  "primary_2(primary)",
		"s":  "PRIMARY(id),a(a),a_2(a,b),b(b)",
	} {
		if got := showIndexKeyNames(t, executor, table); got != want {
			t.Fatalf("SHOW INDEX FROM %s key names = %s, want %s", table, got, want)
		}
	}
}

func showIndexKeyNames(t *testing.T, executor *textStatementExecutor, table string) string {
	t.Helper()
	result, err := executeStatement(executor, "SHOW INDEX FROM "+table)
	if err != nil {
		t.Fatalf("SHOW INDEX FROM %s: %v", table, err)
	}
	order := []string{}
	columns := map[string][]string{}
	for _, row := range result.rows {
		if _, ok := columns[row[2]]; !ok {
			order = append(order, row[2])
		}
		columns[row[2]] = append(columns[row[2]], row[4])
	}
	sort.Strings(order)
	keys := make([]string, len(order))
	for number, name := range order {
		keys[number] = name + "(" + strings.Join(columns[name], ",") + ")"
	}
	return strings.Join(keys, ",")
}
