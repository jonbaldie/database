package mysql

import "testing"

// Issue 335: an INSERT into a self-referencing table with an orphan foreign
// key value reported MySQL error 1451 (parent row changed) instead of 1452
// (child row cannot be added), because child and parent rows live in the same
// table, so the previous parent-rows heuristic always saw the parent as
// changed.
func TestIssue335SelfReferencingInsertViolationCode(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE tree (id INT PRIMARY KEY, parent_id INT, FOREIGN KEY (parent_id) REFERENCES tree(id))"); err != nil {
		t.Fatalf("create tree: %v", err)
	}
	_, err := executeStatement(executor, "INSERT INTO tree VALUES (1, 999)")
	if !isFailureCode(err, 1452) {
		t.Fatalf("orphan self-referencing insert = %v, want 1452", err)
	}
}

// Parent-side violations on a self-referencing table must keep reporting 1451.
func TestIssue335SelfReferencingParentViolationCode(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE tree (id INT PRIMARY KEY, parent_id INT, FOREIGN KEY (parent_id) REFERENCES tree(id))",
		"INSERT INTO tree VALUES (1, NULL), (2, 1), (3, 2)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	for _, failure := range []struct {
		query string
		code  uint16
	}{
		{"DELETE FROM tree WHERE id = 1", 1451},
		{"UPDATE tree SET id = 9 WHERE id = 2", 1451},
		{"UPDATE tree SET parent_id = 99 WHERE id = 3", 1452},
	} {
		if _, err := executeStatement(executor, failure.query); !isFailureCode(err, failure.code) {
			t.Fatalf("self-referencing failure for %q = %v, want %d", failure.query, err, failure.code)
		}
	}
}
