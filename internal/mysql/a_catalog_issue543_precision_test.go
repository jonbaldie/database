package mysql

import "testing"

func TestIssue543NumericAndTemporalPrecision(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE precisions (a TINYINT, b SMALLINT, c MEDIUMINT, d INT UNSIGNED, e BIGINT, f FLOAT, g DOUBLE, h BIT(5), i BOOLEAN, j DATE, k TIME(6), l TIMESTAMP(2), m YEAR)"); err != nil {
		t.Fatal(err)
	}
	result, err := executeStatement(executor, "SELECT NUMERIC_PRECISION, NUMERIC_SCALE, DATETIME_PRECISION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' ORDER BY ORDINAL_POSITION")
	want := [][]string{
		{"3", "0", "NULL"}, {"5", "0", "NULL"}, {"7", "0", "NULL"}, {"10", "0", "NULL"}, {"19", "0", "NULL"},
		{"12", "NULL", "NULL"}, {"22", "NULL", "NULL"}, {"5", "NULL", "NULL"}, {"3", "0", "NULL"},
		{"NULL", "NULL", "0"}, {"NULL", "NULL", "6"}, {"NULL", "NULL", "2"}, {"4", "0", "NULL"},
	}
	if err != nil || !equalRows(result.rows, want) {
		t.Fatalf("precision = %#v, %v; want %#v", result, err, want)
	}
}

func TestIssue543CharacterLengthsAndSQLNullDefault(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE strings (a CHAR(3), b BINARY(3), c TINYTEXT, d TEXT, e MEDIUMBLOB, f LONGTEXT, g INT DEFAULT NULL, h VARCHAR(4) DEFAULT '')"); err != nil {
		t.Fatal(err)
	}
	result, err := executeStatement(executor, "SELECT CHARACTER_MAXIMUM_LENGTH, CHARACTER_OCTET_LENGTH FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' ORDER BY ORDINAL_POSITION")
	want := [][]string{{"3", "12"}, {"3", "3"}, {"255", "255"}, {"65535", "65535"}, {"16777215", "16777215"}, {"4294967295", "4294967295"}, {"NULL", "NULL"}, {"4", "16"}}
	if err != nil || !equalRows(result.rows, want) {
		t.Fatalf("length = %#v, %v; want %#v", result, err, want)
	}
	result, err = executeStatement(executor, "SELECT COLUMN_DEFAULT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' AND (COLUMN_NAME = 'g' OR COLUMN_NAME = 'h') ORDER BY ORDINAL_POSITION")
	if err != nil || !equalNulls(result.nulls, [][]bool{{true}, {false}}) || !equalRows(result.rows, [][]string{{"NULL"}, {""}}) {
		t.Fatalf("defaults = %#v, %v", result, err)
	}
}
