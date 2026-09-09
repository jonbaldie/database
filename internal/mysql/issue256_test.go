package mysql

import (
	"strings"
	"testing"
)

// TestIssue256ModifyColumnPreservesNullability covers the three nullability
// forms of ALTER TABLE ... MODIFY COLUMN: an omitted clause preserves the
// existing constraint, an explicit NULL makes the column nullable, and an
// explicit NOT NULL keeps the constraint.
func TestIssue256ModifyColumnPreservesNullability(t *testing.T) {
	cases := []struct {
		name            string
		create          string
		alter           string
		nullable        bool
		violationColumn string
	}{
		{
			name:            "omitted_clause_preserves_not_null",
			create:          "CREATE TABLE t (id INT PRIMARY KEY, val INT NOT NULL)",
			alter:           "ALTER TABLE t MODIFY val BIGINT",
			nullable:        false,
			violationColumn: "val",
		},
		{
			name:     "omitted_clause_preserves_nullable",
			create:   "CREATE TABLE t (id INT PRIMARY KEY, val INT)",
			alter:    "ALTER TABLE t MODIFY val BIGINT",
			nullable: true,
		},
		{
			name:     "explicit_null_makes_column_nullable",
			create:   "CREATE TABLE t (id INT PRIMARY KEY, val INT NOT NULL)",
			alter:    "ALTER TABLE t MODIFY val BIGINT NULL",
			nullable: true,
		},
		{
			name:            "explicit_not_null_keeps_constraint",
			create:          "CREATE TABLE t (id INT PRIMARY KEY, val INT)",
			alter:           "ALTER TABLE t MODIFY val BIGINT NOT NULL",
			nullable:        false,
			violationColumn: "val",
		},
		{
			name:            "change_omitted_clause_preserves_not_null",
			create:          "CREATE TABLE t (id INT PRIMARY KEY, val INT NOT NULL)",
			alter:           "ALTER TABLE t CHANGE val amount BIGINT",
			nullable:        false,
			violationColumn: "amount",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range []string{test.create, "INSERT INTO t VALUES (1, 42)"} {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("execute %q: %v", query, err)
				}
			}
			if _, err := executeStatement(executor, test.alter); err != nil {
				t.Fatalf("alter %q: %v", test.alter, err)
			}
			_, err := executeStatement(executor, "INSERT INTO t VALUES (2, NULL)")
			if test.nullable {
				if err != nil {
					t.Fatalf("insert into nullable column: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("NOT NULL constraint silently dropped by MODIFY COLUMN")
			}
			if !strings.Contains(err.Error(), "Column '"+test.violationColumn+"' cannot be null") {
				t.Fatalf("error = %v, want column %q cannot be null", err, test.violationColumn)
			}
		})
	}
}
