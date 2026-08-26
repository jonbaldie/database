package mysql

import "testing"

func TestInsertEvaluatesScalarExpressions(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, note VARCHAR(32) NOT NULL, n INT NOT NULL)",
		"INSERT INTO items VALUES (1, CONCAT('a', 'b'), 1 + 1)",
		"INSERT INTO items SET id = 2, note = CONCAT('c', 'd'), n = 3 * 4",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, note, n FROM items ORDER BY id")
	if err != nil || !equalRows(result.rows, [][]string{{"1", "ab", "2"}, {"2", "cd", "12"}}) {
		t.Fatalf("inserted rows = %#v, err = %v", result, err)
	}
}

func TestUpdateRejectsUnknownAssignmentColumn(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, note VARCHAR(32) NOT NULL)",
		"INSERT INTO items VALUES (1, 'keep')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "UPDATE items SET note = missing WHERE id = 99"); !isFailureCode(err, 1054) {
		t.Fatalf("missing assignment column: %v", err)
	}
}

func TestInsertAcceptsBitLiteral(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE flags (id INT PRIMARY KEY, mask BIT(8) NOT NULL)",
		"INSERT INTO flags VALUES (1, b'101')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, mask FROM flags")
	if err != nil || !equalRows(result.rows, [][]string{{"1", "5"}}) {
		t.Fatalf("bit rows = %#v, err = %v", result, err)
	}
}

func TestUpdateEvaluatesAssignmentExpressions(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, note VARCHAR(32) NOT NULL)",
		"INSERT INTO items VALUES (1, 'keep'), (2, 'keep')",
		"UPDATE items SET note = CONCAT('x', 'y') WHERE id = 1",
		"UPDATE items SET note = id + 1 WHERE id = 2",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, note FROM items ORDER BY id")
	if err != nil || !equalRows(result.rows, [][]string{{"1", "xy"}, {"2", "3"}}) {
		t.Fatalf("updated rows = %#v, err = %v", result, err)
	}
}
