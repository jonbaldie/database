package mysql

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

// TestIssue484UnnamedUniqueKeysUseMySQLNames checks that unnamed UNIQUE keys
// use the MySQL 8.4.11 first-column name, with _2, _3 suffixes when that key
// name is already taken, including a second key that starts on the same column.
func TestIssue484UnnamedUniqueKeysUseMySQLNames(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE n8 (id INT PRIMARY KEY, a INT, b INT, UNIQUE (a), UNIQUE (a, b))",
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE inline_unique (code INT UNIQUE)",
		"CREATE TABLE unique_first (a INT, b INT, UNIQUE (a), INDEX (a))",
		"CREATE TABLE check_same_name (a INT, b INT, CONSTRAINT a CHECK (a > 0), UNIQUE (a))",
		"CREATE TABLE check_then_unique (id INT, code INT, CONSTRAINT code CHECK (code > 0))",
		"ALTER TABLE check_then_unique ADD UNIQUE (code)",
		"CREATE TABLE reserved_primary (`primary` INT UNIQUE)",
		"CREATE TABLE explicit_gap (a INT, INDEX a_2 (a), UNIQUE (a), UNIQUE (a))",
		"CREATE TABLE added (id INT PRIMARY KEY, code INT)",
		"ALTER TABLE added ADD UNIQUE (code)",
		"ALTER TABLE added ADD UNIQUE (code)",
		"CREATE TABLE indexed (id INT, code INT, INDEX code (id))",
		"ALTER TABLE indexed ADD UNIQUE (code)",
		longUniqueNames(),
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	assertShowIndexKeys(t, executor, "n8", []string{"PRIMARY", "a", "a_2", "a_2"})
	assertShowIndexKeys(t, executor, "p", []string{"PRIMARY", "code"})
	assertShowIndexKeys(t, executor, "inline_unique", []string{"code"})
	assertShowIndexKeys(t, executor, "unique_first", []string{"a", "a_2"})
	assertShowIndexKeys(t, executor, "check_same_name", []string{"a"})
	assertShowIndexKeys(t, executor, "check_then_unique", []string{"code"})
	assertShowIndexKeys(t, executor, "indexed", []string{"code_2", "code"})
	assertShowIndexKeys(t, executor, "reserved_primary", []string{"primary_2"})
	assertShowIndexKeys(t, executor, "explicit_gap", []string{"a", "a_3", "a_2"})
	assertShowIndexKeys(t, executor, "added", []string{"PRIMARY", "code", "code_2"})
	column := strings.Repeat("n", 64)
	assertShowIndexKeys(t, executor, "long_names", []string{"PRIMARY", column, strings.Repeat("n", 61) + "_2"})

	created, err := executeStatement(executor, "SHOW CREATE TABLE n8")
	if err != nil {
		t.Fatalf("SHOW CREATE TABLE n8: %v", err)
	}
	definition := created.rows[0][1]
	for _, want := range []string{"CONSTRAINT `a` UNIQUE (`a`)", "CONSTRAINT `a_2` UNIQUE (`a`, `b`)"} {
		if !strings.Contains(definition, want) {
			t.Fatalf("SHOW CREATE TABLE n8 = %q, missing %q", definition, want)
		}
	}
	if strings.Contains(definition, "n8_a_unique") {
		t.Fatalf("SHOW CREATE TABLE n8 still uses a non-MySQL key name: %s", definition)
	}
}

func TestIssue484DuplicateExplicitNamesFail(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE same_unique (a INT, b INT, CONSTRAINT a UNIQUE (a), CONSTRAINT a UNIQUE (b))",
		"CREATE TABLE same_check (a INT, CHECK (a > 0), CONSTRAINT same_check_chk_1 CHECK (a < 10))",
	} {
		if _, err := executeStatement(executor, query); err == nil {
			t.Fatalf("%s succeeded", query)
		}
	}
}

func TestIssue484SameTypeConstraintNamesStayUnique(t *testing.T) {
	table := catalog.Table{
		Name:        "t",
		Columns:     []string{"id"},
		ColumnTypes: []string{"INT"},
		Constraints: []catalog.Constraint{
			{Name: "PRIMARY", Type: catalog.ConstraintTypePrimary, Columns: []string{"id"}},
			{Name: "PRIMARY", Type: catalog.ConstraintTypePrimary, Columns: []string{"id"}},
		},
	}
	err := validateConstraintDeclarations(catalog.Definition{}, catalog.Definition{}, "app", "t", table, map[string]int{"id": 0})
	if err == nil || !strings.Contains(err.Error(), "duplicate constraint name 'PRIMARY'") {
		t.Fatalf("duplicate primary names: %v", err)
	}
}

func longUniqueNames() string {
	column := strings.Repeat("n", 64)
	return fmt.Sprintf("CREATE TABLE long_names (id INT PRIMARY KEY, `%s` INT, UNIQUE (`%s`), UNIQUE (`%s`))", column, column, column)
}

func assertShowIndexKeys(t *testing.T, executor *textStatementExecutor, table string, want []string) {
	t.Helper()
	result, err := executeStatement(executor, "SHOW INDEX FROM "+table)
	if err != nil {
		t.Fatalf("SHOW INDEX FROM %s: %v", table, err)
	}
	got := make([]string, len(result.rows))
	for index, row := range result.rows {
		got[index] = row[2]
	}
	if len(got) != len(want) {
		t.Fatalf("SHOW INDEX FROM %s keys = %v, want %v", table, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("SHOW INDEX FROM %s keys = %v, want %v", table, got, want)
		}
	}
}
