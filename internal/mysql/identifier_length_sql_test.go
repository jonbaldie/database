package mysql

import (
	"strings"
	"testing"
)

func TestSavepointNameRejectsOverlongIdentifier(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "BEGIN"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	name := strings.Repeat("s", 65)
	if _, err := executeStatement(executor, "SAVEPOINT "+name); !isFailureCode(err, 1059) {
		t.Fatalf("65-scalar savepoint name error = %v", err)
	}
	if _, err := executeStatement(executor, "SAVEPOINT "+strings.Repeat("s", 64)); err != nil {
		t.Fatalf("64-scalar savepoint name: %v", err)
	}
}

func TestCTENameRejectsOverlongIdentifier(t *testing.T) {
	executor := ddlExecutorForTest(t)
	name := strings.Repeat("w", 65)
	query := "WITH " + name + " AS (SELECT 1 AS id) SELECT id FROM " + name
	if _, err := executeStatement(executor, query); !isFailureCode(err, 1059) {
		t.Fatalf("65-scalar CTE name error = %v", err)
	}
	allowed := strings.Repeat("w", 64)
	if _, err := executeStatement(executor, "WITH "+allowed+" AS (SELECT 1 AS id) SELECT id FROM "+allowed); err != nil {
		t.Fatalf("64-scalar CTE name: %v", err)
	}
}
