package mysql

import (
	"errors"
	"testing"
)

func TestIssue491ColumnCheckRejectsReferenceToAnotherColumn(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE c7 (id INT, x INT CHECK (id > 0))",
		"CREATE TABLE c8 (id INT, x INT CHECK (x > 0 AND id > 0))",
		"CREATE TABLE c9 (id INT, x INT CHECK (x IS NULL OR c9.id > 0))",
	} {
		_, err := executeStatement(executor, query)
		var failure sqlFailure
		if !errors.As(err, &failure) || failure.code != 3813 {
			t.Fatalf("%q error = %v, want 3813", query, err)
		}
	}
	if _, err := executeStatement(executor, "SELECT * FROM c7"); err == nil {
		t.Fatal("rejected CREATE TABLE c7 left a table behind")
	}

	if _, err := executeStatement(executor, "CREATE TABLE base (id INT)"); err != nil {
		t.Fatalf("create base: %v", err)
	}
	_, err := executeStatement(executor, "ALTER TABLE base ADD COLUMN x INT CHECK (id > 0)")
	var failure sqlFailure
	if !errors.As(err, &failure) || failure.code != 3813 {
		t.Fatalf("ALTER TABLE ADD COLUMN error = %v, want 3813", err)
	}

	for _, query := range []string{
		"CREATE TABLE ok1 (id INT, x INT CHECK (x > 0))",
		"CREATE TABLE ok2 (id INT, x INT CHECK (`X` > 0 OR x IS NULL))",
		"CREATE TABLE ok3 (id INT, x INT, CHECK (id < x))",
		"CREATE TABLE ok4 (id INT, x VARCHAR(10) CHECK (CHAR_LENGTH(x) > 0))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%q: %v", query, err)
		}
	}
}
