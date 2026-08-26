package mysql

import "testing"

func TestShowTablesAndDatabasesLike(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY)",
		"CREATE TABLE letters (id INT PRIMARY KEY)",
		"CREATE DATABASE other",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SHOW TABLES LIKE 'item%'")
	if err != nil || !equalRows(result.rows, [][]string{{"items"}}) {
		t.Fatalf("show tables like = %#v, err = %v", result, err)
	}
	result, err = executeStatement(executor, "SHOW DATABASES LIKE 'app'")
	if err != nil || !equalRows(result.rows, [][]string{{"app"}}) {
		t.Fatalf("show databases like = %#v, err = %v", result, err)
	}
}

func TestSelectWhereLike(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE letters (id INT PRIMARY KEY, code VARCHAR(16) NOT NULL)",
		"INSERT INTO letters VALUES (1, 'Ada'), (2, 'Bea')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM letters WHERE code LIKE 'A%'")
	if err != nil || !equalRows(result.rows, [][]string{{"1"}}) {
		t.Fatalf("where like = %#v, err = %v", result, err)
	}
}
