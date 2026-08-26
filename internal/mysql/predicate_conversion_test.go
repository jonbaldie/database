package mysql

import "testing"

func TestEqualityPredicateRequiresExplicitCrossFamilyCast(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, note VARCHAR(32) NOT NULL)",
		"INSERT INTO items VALUES (1, 'keep')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "SELECT id FROM items WHERE note = 1"); !isFailureCode(err, 1292) {
		t.Fatalf("character = integer error = %v", err)
	}
	if _, err := executeStatement(executor, "SELECT id FROM items WHERE id = '1'"); !isFailureCode(err, 1292) {
		t.Fatalf("integer = character error = %v", err)
	}
	result, err := executeStatement(executor, "SELECT id FROM items WHERE note = 'keep' AND id = 1")
	if err != nil || !equalRows(result.rows, [][]string{{"1"}}) {
		t.Fatalf("same-family predicate = %#v, err = %v", result, err)
	}
	if _, err := executeStatement(executor, "UPDATE items SET note = 'x' WHERE note = 1"); !isFailureCode(err, 1292) {
		t.Fatalf("update character = integer error = %v", err)
	}
}
