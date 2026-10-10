package mysql

import (
	"strings"
	"testing"
)

func TestIssue543ContractedTableAndColumnFacts(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query string
		want  [][]string
	}{
		{"SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app'", [][]string{{"DATABASE"}}},
		{"SELECT TABLE_ROWS FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app'", [][]string{{"0"}}},
		{"SELECT IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 'p' ORDER BY ORDINAL_POSITION", [][]string{{"NO", "NULL", "PRI"}, {"YES", "NULL", "MUL"}}},
	} {
		t.Run(test.query, func(t *testing.T) {
			result, err := executeStatement(executor, test.query)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(test.query, "COLUMN_DEFAULT") && !equalNulls(result.nulls, [][]bool{{false, true, false}, {false, true, false}}) {
				t.Fatalf("nulls = %#v", result.nulls)
			}
			if !equalRows(result.rows, test.want) {
				t.Fatalf("rows = %#v, want %#v", result.rows, test.want)
			}
		})
	}
}
