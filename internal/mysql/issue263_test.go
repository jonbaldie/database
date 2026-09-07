package mysql

import (
	"reflect"
	"testing"
)

func TestIssue263UniqueConstraintWithoutSpace(t *testing.T) {
	executor := ddlExecutorForTest(t)

	t.Run("bare UNIQUE without space in CREATE TABLE", func(t *testing.T) {
		query := "CREATE TABLE t1 (id INT PRIMARY KEY, name VARCHAR(20), UNIQUE(name))"
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}

		result, err := executeStatement(executor, "SELECT * FROM t1")
		if err != nil {
			t.Fatalf("SELECT * FROM t1: %v", err)
		}
		wantColumns := []string{"id", "name"}
		if !reflect.DeepEqual(result.columns, wantColumns) {
			t.Fatalf("columns = %v, want %v", result.columns, wantColumns)
		}

		if _, err := executeStatement(executor, "INSERT INTO t1 VALUES (1, 'Alice')"); err != nil {
			t.Fatalf("INSERT into t1: %v", err)
		}
		if _, err := executeStatement(executor, "INSERT INTO t1 VALUES (2, 'Bob')"); err != nil {
			t.Fatalf("INSERT into t1: %v", err)
		}
		if _, err := executeStatement(executor, "INSERT INTO t1 VALUES (3, 'Alice')"); !isFailureCode(err, 1062) {
			t.Fatalf("expected duplicate key error 1062, got %v", err)
		}
	})

	t.Run("column names starting with unique prefix are preserved", func(t *testing.T) {
		query := "CREATE TABLE t2 (id INT PRIMARY KEY, unique_code VARCHAR(20), uniqueness INT)"
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}

		result, err := executeStatement(executor, "SELECT * FROM t2")
		if err != nil {
			t.Fatalf("SELECT * FROM t2: %v", err)
		}
		wantColumns := []string{"id", "unique_code", "uniqueness"}
		if !reflect.DeepEqual(result.columns, wantColumns) {
			t.Fatalf("columns = %v, want %v", result.columns, wantColumns)
		}

		if _, err := executeStatement(executor, "INSERT INTO t2 VALUES (1, 'ABC', 10)"); err != nil {
			t.Fatalf("INSERT into t2: %v", err)
		}
	})

	t.Run("ALTER TABLE ADD UNIQUE without space", func(t *testing.T) {
		query := "CREATE TABLE t3 (id INT PRIMARY KEY, name VARCHAR(20))"
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}

		if _, err := executeStatement(executor, "ALTER TABLE t3 ADD UNIQUE(name)"); err != nil {
			t.Fatalf("ALTER TABLE ADD UNIQUE(name): %v", err)
		}

		if _, err := executeStatement(executor, "INSERT INTO t3 VALUES (1, 'Alice')"); err != nil {
			t.Fatalf("INSERT into t3: %v", err)
		}
		if _, err := executeStatement(executor, "INSERT INTO t3 VALUES (2, 'Alice')"); !isFailureCode(err, 1062) {
			t.Fatalf("expected duplicate key error 1062, got %v", err)
		}
	})
}
