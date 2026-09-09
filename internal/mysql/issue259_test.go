package mysql

import (
	"strings"
	"testing"
)

// TestIssue259DropIndexedColumn follows MySQL: dropping a column removes that
// column from every index and constraint of which it is a part, and removes
// the index or constraint entirely once no parts remain.
func TestIssue259DropIndexedColumn(t *testing.T) {
	cases := []struct {
		name        string
		setup       []string
		alter       string
		selectSQL   string
		want        [][]string
		wantAbsent  []string // index or constraint names that must disappear
		wantPresent []string // index or constraint names that must survive
	}{
		{
			name:       "secondary_index_dropped",
			setup:      []string{"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(20), age INT, INDEX(name))", "INSERT INTO t VALUES (1, 'Alice', 30), (2, 'Bob', 25)"},
			alter:      "ALTER TABLE t DROP COLUMN name",
			selectSQL:  "SELECT id, age FROM t ORDER BY id",
			want:       [][]string{{"1", "30"}, {"2", "25"}},
			wantAbsent: []string{"name"},
		},
		{
			name:       "unique_constraint_dropped",
			setup:      []string{"CREATE TABLE t (id INT PRIMARY KEY, email VARCHAR(40) UNIQUE, age INT)", "INSERT INTO t VALUES (1, 'a@x.test', 30)"},
			alter:      "ALTER TABLE t DROP COLUMN email",
			selectSQL:  "SELECT id, age FROM t",
			want:       [][]string{{"1", "30"}},
			wantAbsent: []string{"email"},
		},
		{
			name:       "primary_key_dropped",
			setup:      []string{"CREATE TABLE t (token VARCHAR(10) PRIMARY KEY, n INT)", "INSERT INTO t VALUES ('k1', 7)"},
			alter:      "ALTER TABLE t DROP COLUMN token",
			selectSQL:  "SELECT n FROM t",
			want:       [][]string{{"7"}},
			wantAbsent: []string{"PRIMARY"},
		},
		{
			name:        "multi_column_index_keeps_remaining_part",
			setup:       []string{"CREATE TABLE t (id INT PRIMARY KEY, a INT, b INT, INDEX ab (a, b))", "INSERT INTO t VALUES (1, 2, 3)"},
			alter:       "ALTER TABLE t DROP COLUMN a",
			selectSQL:   "SELECT id, b FROM t",
			want:        [][]string{{"1", "3"}},
			wantPresent: []string{"ab"},
		},
		{
			name:        "remaining_secondary_index_survives",
			setup:       []string{"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(20), age INT, INDEX(name), INDEX(age))", "INSERT INTO t VALUES (1, 'Alice', 30)"},
			alter:       "ALTER TABLE t DROP COLUMN name",
			selectSQL:   "SELECT id, age FROM t",
			want:        [][]string{{"1", "30"}},
			wantAbsent:  []string{"name"},
			wantPresent: []string{"age"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range append(test.setup, test.alter) {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("execute %q: %v", query, err)
				}
			}
			result, err := executeStatement(executor, test.selectSQL)
			if err != nil {
				t.Fatalf("select after drop: %v", err)
			}
			if !equalRows(result.rows, test.want) {
				t.Fatalf("rows after drop = %#v, want %#v", result.rows, test.want)
			}
			definition, err := executeStatement(executor, "SHOW CREATE TABLE t")
			if err != nil {
				t.Fatalf("show create table: %v", err)
			}
			text := strings.Join(definition.rows[0], "\n")
			for _, name := range test.wantAbsent {
				if strings.Contains(text, name) {
					t.Errorf("create table still references %q:\n%s", name, text)
				}
			}
			for _, name := range test.wantPresent {
				if !strings.Contains(text, name) {
					t.Errorf("create table lost %q:\n%s", name, text)
				}
			}
		})
	}
}
