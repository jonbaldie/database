package mysql

import (
	"strings"
	"testing"
)

func TestShowTablesFromInAndFullForms(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY)",
		"CREATE DATABASE other",
		"CREATE TABLE other.letters (id INT)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	from, err := executeStatement(executor, "SHOW TABLES FROM app")
	if err != nil || !equalRows(from.rows, [][]string{{"items"}}) {
		t.Fatalf("SHOW TABLES FROM app = %#v, err = %v", from, err)
	}
	if !equalSlices(from.columns, []string{"Tables_in_app"}) {
		t.Fatalf("SHOW TABLES FROM columns = %#v", from.columns)
	}

	in, err := executeStatement(executor, "SHOW TABLES IN other")
	if err != nil || !equalRows(in.rows, [][]string{{"letters"}}) {
		t.Fatalf("SHOW TABLES IN other = %#v, err = %v", in, err)
	}

	full, err := executeStatement(executor, "SHOW FULL TABLES")
	if err != nil || !equalSlices(full.columns, []string{"Tables_in_app", "Table_type"}) || !equalRows(full.rows, [][]string{{"items", "BASE TABLE"}}) {
		t.Fatalf("SHOW FULL TABLES = %#v, err = %v", full, err)
	}

	fullFrom, err := executeStatement(executor, "SHOW FULL TABLES FROM other")
	if err != nil || !equalRows(fullFrom.rows, [][]string{{"letters", "BASE TABLE"}}) {
		t.Fatalf("SHOW FULL TABLES FROM other = %#v, err = %v", fullFrom, err)
	}

	like, err := executeStatement(executor, "SHOW TABLES FROM app LIKE 'item%'")
	if err != nil || !equalRows(like.rows, [][]string{{"items"}}) {
		t.Fatalf("SHOW TABLES FROM LIKE = %#v, err = %v", like, err)
	}

	where, err := executeStatement(executor, "SHOW TABLES WHERE Tables_in_app = 'items'")
	if err != nil || !equalRows(where.rows, [][]string{{"items"}}) {
		t.Fatalf("SHOW TABLES WHERE = %#v, err = %v", where, err)
	}
}

func TestShowTablesFromInformationSchema(t *testing.T) {
	executor := ddlExecutorForTest(t)
	result, err := executeStatement(executor, "SHOW TABLES FROM information_schema")
	if err != nil {
		t.Fatalf("SHOW TABLES FROM information_schema: %v", err)
	}
	baseline, err := executeStatement(executor, "USE information_schema")
	if err != nil {
		t.Fatalf("USE information_schema: %v", err)
	}
	_ = baseline
	current, err := executeStatement(executor, "SHOW TABLES")
	if err != nil || !equalRows(result.rows, current.rows) {
		t.Fatalf("FROM information_schema = %#v, current = %#v, err = %v", result, current, err)
	}

	full, err := executeStatement(executor, "SHOW FULL TABLES FROM information_schema")
	if err != nil || len(full.rows) == 0 || len(full.rows[0]) != 2 || full.rows[0][1] != "SYSTEM VIEW" {
		t.Fatalf("SHOW FULL TABLES FROM information_schema = %#v, err = %v", full, err)
	}
}

func TestShowCharacterSetAndCollation(t *testing.T) {
	executor := ddlExecutorForTest(t)
	charset, err := executeStatement(executor, "SHOW CHARACTER SET")
	if err != nil || !equalSlices(charset.columns, []string{"Charset", "Description", "Default collation", "Maxlen"}) {
		t.Fatalf("SHOW CHARACTER SET columns = %#v, err = %v", charset, err)
	}
	if !equalRows(charset.rows, [][]string{{"utf8mb4", "UTF-8 Unicode", "utf8mb4_0900_ai_ci", "4"}}) {
		t.Fatalf("SHOW CHARACTER SET rows = %#v", charset.rows)
	}

	like, err := executeStatement(executor, "SHOW CHARSET LIKE 'utf%'")
	if err != nil || !equalRows(like.rows, charset.rows) {
		t.Fatalf("SHOW CHARSET LIKE = %#v, err = %v", like, err)
	}

	collation, err := executeStatement(executor, "SHOW COLLATION")
	if err != nil || !equalSlices(collation.columns, []string{"Collation", "Charset", "Id", "Default", "Compiled", "Sortlen", "Pad_attribute"}) {
		t.Fatalf("SHOW COLLATION columns = %#v, err = %v", collation, err)
	}
	if !equalRows(collation.rows, [][]string{
		{"utf8mb4_0900_ai_ci", "utf8mb4", "255", "Yes", "Yes", "0", "NO PAD"},
		{"utf8mb4_bin", "utf8mb4", "46", "", "Yes", "1", "PAD SPACE"},
	}) {
		t.Fatalf("SHOW COLLATION rows = %#v", collation.rows)
	}

	where, err := executeStatement(executor, "SHOW COLLATION WHERE Charset = 'utf8mb4'")
	if err != nil || !equalRows(where.rows, collation.rows) {
		t.Fatalf("SHOW COLLATION WHERE = %#v, err = %v", where, err)
	}
}

func TestShowIndexAndColumnsFromDatabaseClause(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, label VARCHAR(8))",
		"CREATE DATABASE other",
		"CREATE TABLE other.letters (id INT PRIMARY KEY)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	indexes, err := executeStatement(executor, "SHOW INDEX FROM letters FROM other")
	if err != nil || len(indexes.rows) != 1 || indexes.rows[0][0] != "letters" || indexes.rows[0][2] != "PRIMARY" {
		t.Fatalf("SHOW INDEX FROM letters FROM other = %#v, err = %v", indexes, err)
	}

	in, err := executeStatement(executor, "SHOW KEYS FROM letters IN other")
	if err != nil || !equalRows(in.rows, indexes.rows) {
		t.Fatalf("SHOW KEYS FROM letters IN other = %#v, err = %v", in, err)
	}

	columns, err := executeStatement(executor, "SHOW COLUMNS FROM letters FROM other")
	if err != nil || !equalRows(columns.rows, [][]string{{"id", "INT", "NO", "PRI", "", ""}}) {
		t.Fatalf("SHOW COLUMNS FROM letters FROM other = %#v, err = %v", columns, err)
	}

	where, err := executeStatement(executor, "SHOW INDEX FROM items WHERE Key_name = 'PRIMARY'")
	if err != nil || len(where.rows) != 1 || where.rows[0][2] != "PRIMARY" {
		t.Fatalf("SHOW INDEX WHERE = %#v, err = %v", where, err)
	}
}

func TestShowDatabasesWhere(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE DATABASE other"); err != nil {
		t.Fatalf("create database: %v", err)
	}
	result, err := executeStatement(executor, "SHOW DATABASES WHERE Database = 'other'")
	if err != nil || !equalRows(result.rows, [][]string{{"other"}}) {
		t.Fatalf("SHOW DATABASES WHERE = %#v, err = %v", result, err)
	}
}

func TestShowCatalogUnsupportedDatabase(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "SHOW TABLES FROM missing"); err == nil || !strings.Contains(err.Error(), "unknown database") {
		t.Fatalf("SHOW TABLES FROM missing err = %v, want unknown database", err)
	}
}
