package mysql

import "testing"

func TestIssue226CheckConstraintWithoutSpace(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE unnamed_check (id INT PRIMARY KEY, value INT, CHECK(value > 0 AND value < 100))",
		"CREATE TABLE named_check (id INT PRIMARY KEY, value INT, CONSTRAINT value_range CHECK(value > 0 AND value < 100))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	for _, query := range []string{
		"INSERT INTO unnamed_check VALUES (1, 10)",
		"INSERT INTO unnamed_check VALUES (2, NULL)",
		"INSERT INTO named_check VALUES (1, 10)",
		"INSERT INTO named_check VALUES (2, NULL)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute valid or unknown check %q: %v", query, err)
		}
	}

	for _, query := range []string{
		"INSERT INTO unnamed_check VALUES (3, 0)",
		"INSERT INTO named_check VALUES (3, 100)",
	} {
		if _, err := executeStatement(executor, query); !isFailureCode(err, 3819) {
			t.Errorf("execute invalid check %q error = %v, want MySQL error 3819", query, err)
		}
	}
}
