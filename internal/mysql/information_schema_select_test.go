package mysql

import "testing"

func TestInformationSchemaSelectSupportsClauses(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE nums (id INT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	result, err := executeStatement(executor, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app' ORDER BY TABLE_NAME")
	if err != nil || !equalRows(result.rows, [][]string{{"nums"}}) {
		t.Fatalf("WHERE/ORDER BY = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app' LIMIT 1")
	if err != nil || !equalRows(result.rows, [][]string{{"nums"}}) {
		t.Fatalf("LIMIT = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT t.TABLE_NAME FROM information_schema.TABLES AS t WHERE t.TABLE_NAME = 'nums'")
	if err != nil || !equalRows(result.rows, [][]string{{"nums"}}) {
		t.Fatalf("alias = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT TABLE_SCHEMA, COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app' GROUP BY TABLE_SCHEMA")
	if err != nil || len(result.rows) != 1 || result.rows[0][0] != "app" || result.rows[0][1] != "1" {
		t.Fatalf("GROUP BY = %#v, %v", result, err)
	}
}

func TestInformationSchemaExposesContractedViews(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE parents (id INT PRIMARY KEY)",
		"CREATE TABLE children (id INT PRIMARY KEY, parent_id INT, UNIQUE (parent_id), FOREIGN KEY (parent_id) REFERENCES parents(id), CHECK (id > 0))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	views := []string{
		"STATISTICS", "TABLE_CONSTRAINTS", "KEY_COLUMN_USAGE", "REFERENTIAL_CONSTRAINTS",
		"CHECK_CONSTRAINTS", "CHARACTER_SETS", "COLLATIONS", "ACCOUNTS", "ACCOUNT_GRANTS", "PROCESSLIST",
	}
	for _, view := range views {
		result, err := executeStatement(executor, "SELECT * FROM information_schema."+view)
		if err != nil {
			t.Fatalf("SELECT * FROM information_schema.%s: %v", view, err)
		}
		if result == nil {
			t.Fatalf("nil result for %s", view)
		}
	}
	result, err := executeStatement(executor, "SELECT CHARACTER_SET_NAME FROM information_schema.CHARACTER_SETS")
	if err != nil || !equalRows(result.rows, [][]string{{"utf8mb4"}}) {
		t.Fatalf("CHARACTER_SETS = %#v, %v", result, err)
	}
	result, err = executeStatement(executor, "SELECT COLLATION_NAME FROM information_schema.COLLATIONS ORDER BY COLLATION_NAME")
	if err != nil || !equalRows(result.rows, [][]string{{"utf8mb4_0900_ai_ci"}, {"utf8mb4_bin"}}) {
		t.Fatalf("COLLATIONS = %#v, %v", result, err)
	}
}
