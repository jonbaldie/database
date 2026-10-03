package mysql

import "testing"

// TestIssue486ShowIndexNullAndWhereUseDisplayedValues checks that SHOW INDEX
// reports an empty Null for a NOT NULL key column, and that WHERE filters on the
// displayed values, including SQL NULL and numeric columns.
func TestIssue486ShowIndexNullAndWhereUseDisplayedValues(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pid INT, CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES p (id))",
		"CREATE INDEX ic ON c (pid)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	all, err := executeStatement(executor, "SHOW INDEX FROM c")
	if err != nil {
		t.Fatalf("SHOW INDEX FROM c: %v", err)
	}
	nullColumn := 9
	for index, row := range all.rows {
		want := "YES"
		if row[2] == "PRIMARY" {
			want = ""
		}
		if row[nullColumn] != want || all.nulls[index][nullColumn] {
			t.Fatalf("SHOW INDEX row %v Null = %q (SQL NULL %v), want %q", row, row[nullColumn], all.nulls[index][nullColumn], want)
		}
	}

	for _, tc := range []struct {
		where string
		keys  []string
	}{
		{"`Null` IS NULL", nil},
		{"`Null` = 'YES'", []string{"ic"}},
		{"`Null` = ''", []string{"PRIMARY"}},
		{"Sub_part IS NULL", []string{"PRIMARY", "ic"}},
		{"Non_unique = 0", []string{"PRIMARY"}},
		{"Non_unique = 1", []string{"ic"}},
		{"Seq_in_index > 0", []string{"PRIMARY", "ic"}},
	} {
		result, err := executeStatement(executor, "SHOW INDEX FROM c WHERE "+tc.where)
		if err != nil {
			t.Fatalf("SHOW INDEX FROM c WHERE %s: %v", tc.where, err)
		}
		if !sameShowIndexKeys(result.rows, tc.keys) {
			t.Fatalf("SHOW INDEX FROM c WHERE %s rows = %v, want keys %v", tc.where, result.rows, tc.keys)
		}
	}
}

func sameShowIndexKeys(rows [][]string, keys []string) bool {
	if len(rows) != len(keys) {
		return false
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row[2]] = true
	}
	for _, key := range keys {
		if !seen[key] {
			return false
		}
	}
	return true
}
