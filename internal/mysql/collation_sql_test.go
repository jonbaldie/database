package mysql

import "testing"

func TestUtf8mb4BinLikeIsCaseSensitive(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, v VARCHAR(16) COLLATE utf8mb4_bin)",
		"INSERT INTO t VALUES (1, 'ABC')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	mismatch, err := executeStatement(executor, "SELECT id FROM t WHERE v LIKE 'a%'")
	if err != nil {
		t.Fatalf("LIKE a%%: %v", err)
	}
	if len(mismatch.rows) != 0 {
		t.Fatalf("utf8mb4_bin 'ABC' LIKE 'a%%' = %v, want no rows", mismatch.rows)
	}
	match, err := executeStatement(executor, "SELECT id FROM t WHERE v LIKE 'A%'")
	if err != nil {
		t.Fatalf("LIKE A%%: %v", err)
	}
	if len(match.rows) != 1 || match.rows[0][0] != "1" {
		t.Fatalf("utf8mb4_bin 'ABC' LIKE 'A%%' = %v, want [[1]]", match.rows)
	}
}

func TestUtf8mb40900AICILikeIsCaseInsensitive(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, v VARCHAR(16) COLLATE utf8mb4_0900_ai_ci)",
		"INSERT INTO t VALUES (1, 'ABC')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM t WHERE v LIKE 'a%'")
	if err != nil {
		t.Fatalf("LIKE a%%: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("utf8mb4_0900_ai_ci 'ABC' LIKE 'a%%' = %v, want [[1]]", result.rows)
	}
}

func TestMixedCollationColumnComparisonIsError1267(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, ai VARCHAR(16) COLLATE utf8mb4_0900_ai_ci, bin VARCHAR(16) COLLATE utf8mb4_bin)",
		"INSERT INTO t VALUES (1, 'Abc', 'abc')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "SELECT id FROM t WHERE ai = bin"); !isFailureCode(err, 1267) {
		t.Fatalf("ai = bin: %v, want 1267", err)
	}
}

func TestColumnVersusLiteralUsesColumnCollation(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, ai VARCHAR(16) COLLATE utf8mb4_0900_ai_ci, bin VARCHAR(16) COLLATE utf8mb4_bin)",
		"INSERT INTO t VALUES (1, 'Abc', 'Abc')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	ai, err := executeStatement(executor, "SELECT id FROM t WHERE ai = 'abc'")
	if err != nil {
		t.Fatalf("ai = literal: %v", err)
	}
	if len(ai.rows) != 1 || ai.rows[0][0] != "1" {
		t.Fatalf("ai_ci column = 'abc' = %v, want [[1]]", ai.rows)
	}
	bin, err := executeStatement(executor, "SELECT id FROM t WHERE bin = 'abc'")
	if err != nil {
		t.Fatalf("bin = literal: %v", err)
	}
	if len(bin.rows) != 0 {
		t.Fatalf("utf8mb4_bin column = 'abc' = %v, want no rows", bin.rows)
	}
}
