package mysql

import (
	"strings"
	"testing"
)

// Error paths inside applyInsertPlan/applyUpsertPlan/applyReplacePlan must
// return the underlying error; each case below guards one such return.
func TestIssue448ErrorPathsReturnErrors(t *testing.T) {
	executor := newIssue448Executor(t)
	for _, statement := range []string{
		"CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)",
	} {
		assertIssue448OK(t, executor, statement, 0, 0)
	}
	for _, statement := range []string{
		// VALUES count does not match the column list.
		"INSERT INTO ids (id, code) VALUES (1) ON DUPLICATE KEY UPDATE n = 1",
		// Type error inside the candidate row of an upsert.
		"INSERT INTO ids (code, n) VALUES ('x', 'bad') ON DUPLICATE KEY UPDATE n = 1",
		// Type error inside a REPLACE candidate row.
		"REPLACE INTO ids (code, n) VALUES ('y', 'bad')",
		// Unknown table in REPLACE.
		"REPLACE INTO missing (id, code) VALUES (1, 'z')",
		// Unknown column in REPLACE.
		"REPLACE INTO ids (missing) VALUES ('z')",
		// Column-count mismatch in a plain INSERT.
		"INSERT INTO ids (id, code, n) VALUES (1)",
	} {
		if _, _, err := issue448OK(executor, statement); err == nil {
			t.Fatalf("%s returned an OK packet", statement)
		}
	}
}

// An upsert whose update expression cannot be stored must fail even when
// the candidate row itself is valid. The assignment conversion runs only
// on the conflicting row.
func TestIssue448UpsertAssignmentTypeError(t *testing.T) {
	executor := newIssue448Executor(t)
	assertIssue448OK(t, executor, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)", 0, 0)
	assertIssue448OK(t, executor, "INSERT INTO ids (code, n) VALUES ('a', 1)", 1, 1)
	_, _, err := issue448OK(executor, "INSERT INTO ids (code, n) VALUES ('a', 2) ON DUPLICATE KEY UPDATE n = 'bad'")
	if err == nil {
		t.Fatal("bad upsert assignment returned an OK packet")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Fatalf("upsert assignment error = %q", err.Error())
	}
}

// applyInsertPlan must not share its rows backing array with the plan table
// when there is spare capacity to avoid; appending must not corrupt the
// planned rows.
func TestIssue448InsertPlanDoesNotAliasRows(t *testing.T) {
	table := issue448AutoTable()
	backing := make([][]string, 1, 8)
	backing[0] = []string{"1", "a"}
	table.Rows = backing
	plan := insertPlan{
		table:   table,
		columns: []int{0, 1},
		groups:  [][]string{{"NULL", "'b'"}},
	}
	write, err := applyInsertPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(write.rows) != 2 || write.rows[1][0] != "1" {
		t.Fatalf("rows = %#v", write.rows)
	}
	if backing[0][1] != "a" || len(table.Rows) != 1 {
		t.Fatalf("plan rows aliased: %#v", table.Rows)
	}
	// Force the grow path: the backing array cannot hold the new row.
	tight := table
	tight.Rows = [][]string{{"1", "a"}}
	plan2 := insertPlan{table: tight, columns: []int{0, 1}, groups: [][]string{{"NULL", "'b'"}, {"NULL", "'c'"}}}
	write2, err := applyInsertPlan(plan2)
	if err != nil {
		t.Fatal(err)
	}
	if len(write2.rows) != 3 || write2.rows[1][0] != "1" || write2.rows[2][0] != "2" {
		t.Fatalf("rows = %#v", write2.rows)
	}
}

// Updating the AUTO_INCREMENT column through ON DUPLICATE KEY UPDATE must
// advance the counter past the new value and must not itself be recorded
// as an insert id.
func TestIssue448UpsertRatchetsCounter(t *testing.T) {
	executor := newIssue448Executor(t)
	assertIssue448OK(t, executor, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE, n INT)", 0, 0)
	assertIssue448OK(t, executor, "INSERT INTO ids (code, n) VALUES ('a', 1)", 1, 1)
	assertIssue448OK(t, executor, "INSERT INTO ids (code, n) VALUES ('a', 1) ON DUPLICATE KEY UPDATE id = 20", 2, 0)
	assertIssue448OK(t, executor, "INSERT INTO ids (code, n) VALUES ('b', 2)", 1, 21)
}

// A failed multi-row statement keeps its allocated AUTO_INCREMENT values
// consumed; the next statement reports the following id.
func TestIssue448FailedStatementConsumesIDs(t *testing.T) {
	executor := newIssue448Executor(t)
	assertIssue448OK(t, executor, "CREATE TABLE ids (id INT PRIMARY KEY AUTO_INCREMENT, code VARCHAR(8) UNIQUE)", 0, 0)
	assertIssue448OK(t, executor, "INSERT INTO ids (code) VALUES ('a')", 1, 1)
	if _, _, err := issue448OK(executor, "INSERT INTO ids (code) VALUES ('b'), ('a')"); err == nil {
		t.Fatal("duplicate insert returned an OK packet")
	}
	assertIssue448OK(t, executor, "INSERT INTO ids (code) VALUES ('c')", 1, 4)
}
