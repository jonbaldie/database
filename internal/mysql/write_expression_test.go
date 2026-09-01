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

func TestInsertAcceptsEmptyColumnAndValueLists(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE defaults (id INT DEFAULT 7, note VARCHAR(32) DEFAULT 'empty')"); err != nil {
		t.Fatalf("create defaults: %v", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO defaults () VALUES ()"); err != nil {
		t.Fatalf("empty insert: %v", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO defaults VALUES ()"); err != nil {
		t.Fatalf("default-values insert: %v", err)
	}
	result, err := executeStatement(executor, "SELECT id, note FROM defaults")
	if err != nil || !equalRows(result.rows, [][]string{{"7", "empty"}, {"7", "empty"}}) {
		t.Fatalf("default row = %#v, err = %v", result.rows, err)
	}
	if _, err := executeStatement(executor, "INSERT INTO defaults (id) VALUES ()"); !isFailureCode(err, 1136) {
		t.Fatalf("empty value list with explicit column error = %v", err)
	}
}

func TestInsertUsesExplicitDefaultValues(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, count INT DEFAULT 42, status VARCHAR(32) DEFAULT 'ready')",
		"INSERT INTO items (id, count, status) VALUES (1, DEFAULT, DEFAULT), (2, 7, 'manual'), (3, default, 'DEFAULT')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, count, status FROM items ORDER BY id")
	want := [][]string{{"1", "42", "ready"}, {"2", "7", "manual"}, {"3", "42", "DEFAULT"}}
	if err != nil || !equalRows(result.rows, want) {
		t.Fatalf("inserted rows = %#v, err = %v", result.rows, err)
	}
}

func TestInsertDefaultRejectsRequiredColumnWithoutDefault(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE items (id INT PRIMARY KEY, status VARCHAR(32) NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO items VALUES (1, DEFAULT)"); !isFailureCode(err, 1364) {
		t.Fatalf("missing default error = %v", err)
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

func TestUpdateEvaluatesAssignmentsLeftToRight(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, a INT, b INT)",
		"INSERT INTO items VALUES (1, 5, 0)",
		"UPDATE items SET a = a + 1, b = a WHERE id = 1",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, a, b FROM items")
	if err != nil {
		t.Fatalf("select updated row: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"1", "6", "6"}}) {
		t.Fatalf("updated rows = %#v, want [[1 6 6]]", result.rows)
	}
}

func TestUpdateAssignmentOrderAffectsLaterExpressions(t *testing.T) {
	tests := []struct {
		name        string
		initial     string
		assignments string
		want        [][]string
	}{
		{
			name:        "swap reads the first assigned value",
			initial:     "1, 5, 10",
			assignments: "a = b, b = a",
			want:        [][]string{{"1", "10", "10"}},
		},
		{
			name:        "reversed order reads the original value first",
			initial:     "1, 5, 0",
			assignments: "b = a, a = a + 1",
			want:        [][]string{{"1", "6", "5"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range []string{
				"CREATE TABLE items (id INT PRIMARY KEY, a INT, b INT)",
				"INSERT INTO items VALUES (" + test.initial + ")",
				"UPDATE items SET " + test.assignments + " WHERE id = 1",
			} {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("execute %q: %v", query, err)
				}
			}
			result, err := executeStatement(executor, "SELECT id, a, b FROM items")
			if err != nil {
				t.Fatalf("select updated row: %v", err)
			}
			if !equalRows(result.rows, test.want) {
				t.Fatalf("updated rows = %#v, want %#v", result.rows, test.want)
			}
		})
	}
}

func TestUpdateAssignmentFailureLeavesRowUnchanged(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY, a INT, b TINYINT)",
		"INSERT INTO items VALUES (1, 5, 0)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "UPDATE items SET a = a + 1, b = 128 WHERE id = 1"); err == nil {
		t.Fatal("update with invalid later assignment succeeded")
	}
	result, err := executeStatement(executor, "SELECT id, a, b FROM items")
	if err != nil {
		t.Fatalf("select row after failed update: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"1", "5", "0"}}) {
		t.Fatalf("row after failed update = %#v, want [[1 5 0]]", result.rows)
	}
}
