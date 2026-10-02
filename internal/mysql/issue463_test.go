package mysql

import "testing"

func TestDateComparesWithMidnightDatetimeAcrossSubqueryBoundaries(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE d (id INT PRIMARY KEY, v DATE)",
		"CREATE TABLE dt (id INT PRIMARY KEY, v DATETIME)",
		"INSERT INTO d VALUES (1, '2020-01-01')",
		"INSERT INTO dt VALUES (1, '2020-01-01 00:00:00')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, query := range []string{
		"SELECT d.id FROM d JOIN dt ON d.v = dt.v",
		"SELECT id FROM d WHERE v = (SELECT v FROM dt)",
		"SELECT id FROM d WHERE v IN (SELECT v FROM dt)",
		"SELECT id FROM d WHERE (SELECT v FROM dt) = v",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if len(result.rows) != 1 || result.rows[0][0] != "1" {
			t.Fatalf("%s = %v, want [[1]]", query, result.rows)
		}
	}
}

func TestBinaryCollationSurvivesSubqueryBoundaries(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE b (id INT PRIMARY KEY, v VARCHAR(10) COLLATE utf8mb4_bin)",
		"CREATE TABLE u (id INT PRIMARY KEY, v VARCHAR(10))",
		"INSERT INTO b VALUES (1, 'a')",
		"INSERT INTO u VALUES (1, 'A')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, query := range []string{
		"SELECT id FROM b WHERE v = 'A'",
		"SELECT id FROM b WHERE 'A' = (SELECT v FROM b)",
		"SELECT id FROM b WHERE (SELECT v FROM b) = 'A'",
		"SELECT id FROM b WHERE 'A' IN (SELECT v FROM b)",
		"SELECT id FROM b WHERE (SELECT v FROM b) = (SELECT 'A')",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if len(result.rows) != 0 {
			t.Fatalf("%s = %v, want no rows under utf8mb4_bin", query, result.rows)
		}
	}
	for _, query := range []string{
		"SELECT id FROM b WHERE 'a' = (SELECT v FROM b)",
		"SELECT id FROM u WHERE 'a' = (SELECT v FROM u)",
		"SELECT id FROM u WHERE 'a' IN (SELECT v FROM u)",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if len(result.rows) != 1 || result.rows[0][0] != "1" {
			t.Fatalf("%s = %v, want [[1]]", query, result.rows)
		}
	}
}

func TestDateComparesWithMidnightDatetimeInSubqueryList(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE d (id INT PRIMARY KEY, v DATE)",
		"CREATE TABLE dt (id INT PRIMARY KEY, v DATETIME)",
		"INSERT INTO d VALUES (1, '2020-01-01')",
		"INSERT INTO dt VALUES (1, '2020-01-01 00:00:00')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, query := range []string{
		"SELECT id FROM d WHERE v IN (SELECT v FROM dt)",
		"SELECT id FROM dt WHERE v IN (SELECT v FROM d)",
		"SELECT id FROM dt WHERE v = (SELECT v FROM d)",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if len(result.rows) != 1 || result.rows[0][0] != "1" {
			t.Fatalf("%s = %v, want [[1]]", query, result.rows)
		}
	}
}

func TestBinaryCollationSurvivesSetOperationBoundaries(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE b1 (id INT PRIMARY KEY, v VARCHAR(10) COLLATE utf8mb4_bin)",
		"CREATE TABLE b2 (id INT PRIMARY KEY, v VARCHAR(10) COLLATE utf8mb4_bin)",
		"INSERT INTO b1 VALUES (1, 'a')",
		"INSERT INTO b2 VALUES (1, 'A'), (2, 'B')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT v FROM b1 UNION SELECT v FROM b2 ORDER BY v")
	if err != nil {
		t.Fatalf("union: %v", err)
	}
	if len(result.rows) != 3 || result.rows[0][0] != "A" || result.rows[1][0] != "B" || result.rows[2][0] != "a" {
		t.Fatalf("utf8mb4_bin UNION ORDER BY = %v, want [[A] [B] [a]]", result.rows)
	}
}
