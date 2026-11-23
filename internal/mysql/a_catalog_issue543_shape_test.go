package mysql

import (
	"strings"
	"testing"
)

func TestIssue543CatalogViewShapes(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, test := range []struct{ view, columns string }{
		{"TABLES", "TABLE_CATALOG,TABLE_SCHEMA,TABLE_NAME,TABLE_TYPE,ENGINE,VERSION,ROW_FORMAT,TABLE_ROWS,AVG_ROW_LENGTH,DATA_LENGTH,MAX_DATA_LENGTH,INDEX_LENGTH,DATA_FREE,AUTO_INCREMENT,CREATE_TIME,UPDATE_TIME,CHECK_TIME,TABLE_COLLATION,CHECKSUM,CREATE_OPTIONS,TABLE_COMMENT"},
		{"COLUMNS", "TABLE_CATALOG,TABLE_SCHEMA,TABLE_NAME,COLUMN_NAME,ORDINAL_POSITION,COLUMN_DEFAULT,IS_NULLABLE,DATA_TYPE,CHARACTER_MAXIMUM_LENGTH,CHARACTER_OCTET_LENGTH,NUMERIC_PRECISION,NUMERIC_SCALE,DATETIME_PRECISION,CHARACTER_SET_NAME,COLLATION_NAME,COLUMN_TYPE,COLUMN_KEY,EXTRA,PRIVILEGES,COLUMN_COMMENT,GENERATION_EXPRESSION,SRS_ID"},
	} {
		t.Run(test.view, func(t *testing.T) {
			result, err := executeStatement(executor, "SELECT * FROM information_schema."+test.view+" LIMIT 0")
			if err != nil || strings.Join(result.columns, ",") != test.columns {
				t.Fatalf("shape = %#v, %v; want %s", result, err, test.columns)
			}
			result, err = executeStatement(executor, "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = '"+strings.ToLower(test.view)+"' ORDER BY ORDINAL_POSITION")
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, row := range result.rows {
				names = append(names, row[0])
			}
			if strings.Join(names, ",") != test.columns {
				t.Fatalf("self-description = %#v", result.rows)
			}
		})
	}
}

func TestIssue543ColumnTypeFacts(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE facts (id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT, label VARCHAR(12) COLLATE utf8mb4_bin NOT NULL DEFAULT 'hello', amount DECIMAL(10,2) DEFAULT 1.25, stamp DATETIME(3), raw VARBINARY(8))"); err != nil {
		t.Fatal(err)
	}
	result, err := executeStatement(executor, "SELECT DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, CHARACTER_OCTET_LENGTH, NUMERIC_PRECISION, NUMERIC_SCALE, DATETIME_PRECISION, CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' ORDER BY ORDINAL_POSITION")
	want := [][]string{
		{"bigint", "NULL", "NULL", "20", "0", "NULL", "NULL", "NULL"},
		{"varchar", "12", "48", "NULL", "NULL", "NULL", "utf8mb4", "utf8mb4_bin"},
		{"decimal", "NULL", "NULL", "10", "2", "NULL", "NULL", "NULL"},
		{"datetime", "NULL", "NULL", "NULL", "NULL", "3", "NULL", "NULL"},
		{"varbinary", "8", "8", "NULL", "NULL", "NULL", "NULL", "NULL"},
	}
	if err != nil || !equalRows(result.rows, want) {
		t.Fatalf("type facts = %#v, %v; want %#v", result, err, want)
	}
	result, err = executeStatement(executor, "SELECT COLUMN_DEFAULT, COLUMN_KEY, EXTRA, PRIVILEGES, COLUMN_COMMENT, GENERATION_EXPRESSION, SRS_ID FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' ORDER BY ORDINAL_POSITION")
	want = [][]string{
		{"NULL", "PRI", "auto_increment", "select,insert,update,references", "", "", "NULL"},
		{"hello", "", "", "select,insert,update,references", "", "", "NULL"},
		{"1.25", "", "", "select,insert,update,references", "", "", "NULL"},
		{"NULL", "", "", "select,insert,update,references", "", "", "NULL"},
		{"NULL", "", "", "select,insert,update,references", "", "", "NULL"},
	}
	if err != nil || !equalRows(result.rows, want) {
		t.Fatalf("column facts = %#v, %v; want %#v", result, err, want)
	}
}
