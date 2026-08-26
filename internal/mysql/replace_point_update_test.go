package mysql

import "testing"

func TestReplaceThenPointUpdateChangesTheReplacedRow(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, note VARCHAR(32) NOT NULL)",
		"INSERT INTO items VALUES (1, 'n1'), (2, 'n2')",
		"REPLACE INTO items VALUES (1, 'r2')",
		"UPDATE items SET note = 'updated' WHERE id = 1",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, note FROM items ORDER BY id")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"1", "updated"}, {"2", "n2"}}) {
		t.Fatalf("rows after replace then point update = %#v", result.rows)
	}
}
