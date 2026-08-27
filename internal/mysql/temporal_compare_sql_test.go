package mysql

import "testing"

func TestDateEqualsMidnightDatetime(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, d DATE, ts DATETIME)",
		"INSERT INTO t VALUES (1, '2020-01-02', '2020-01-02 00:00:00')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	equal, err := executeStatement(executor, "SELECT id FROM t WHERE d = ts")
	if err != nil {
		t.Fatalf("d = ts: %v", err)
	}
	if len(equal.rows) != 1 || equal.rows[0][0] != "1" {
		t.Fatalf("DATE = midnight DATETIME = %v, want [[1]]", equal.rows)
	}
	less, err := executeStatement(executor, "SELECT id FROM t WHERE d < ts")
	if err != nil {
		t.Fatalf("d < ts: %v", err)
	}
	if len(less.rows) != 0 {
		t.Fatalf("DATE < midnight DATETIME = %v, want no rows", less.rows)
	}
}

func TestDateIsBeforeNoonDatetime(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, d DATE, ts DATETIME)",
		"INSERT INTO t VALUES (1, '2020-01-02', '2020-01-02 12:00:00')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	equal, err := executeStatement(executor, "SELECT id FROM t WHERE d = ts")
	if err != nil {
		t.Fatalf("d = ts: %v", err)
	}
	if len(equal.rows) != 0 {
		t.Fatalf("DATE = noon DATETIME = %v, want no rows", equal.rows)
	}
	less, err := executeStatement(executor, "SELECT id FROM t WHERE d < ts")
	if err != nil {
		t.Fatalf("d < ts: %v", err)
	}
	if len(less.rows) != 1 || less.rows[0][0] != "1" {
		t.Fatalf("DATE < noon DATETIME = %v, want [[1]]", less.rows)
	}
}

func TestDateEqualsMidnightTimestampAtUTC(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"SET time_zone = '+00:00'",
		"CREATE TABLE t (id INT PRIMARY KEY, d DATE, ts TIMESTAMP)",
		"INSERT INTO t VALUES (1, '2020-01-02', '2020-01-02 00:00:00')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM t WHERE d = ts")
	if err != nil {
		t.Fatalf("d = ts: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("DATE = midnight TIMESTAMP = %v, want [[1]]", result.rows)
	}
}
