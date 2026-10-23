package mysql

import (
	"reflect"
	"testing"
)

func TestIssue216MixedOuterJoinChains(t *testing.T) {
	executor := relationalSelectExecutor(t)
	seedSQL(t, executor,
		"CREATE TABLE issue216_a (id INT)",
		"CREATE TABLE issue216_b (id INT, a_ref INT)",
		"CREATE TABLE issue216_c (b_ref INT, label VARCHAR(32))",
		"INSERT INTO issue216_a VALUES (1), (2), (3)",
		"INSERT INTO issue216_b VALUES (10, 1), (20, 2)",
		"INSERT INTO issue216_c VALUES (10, 'first')",
	)

	query := "SELECT a.id, b.id, c.label FROM issue216_a a JOIN issue216_b b ON b.a_ref = a.id LEFT JOIN issue216_c c ON c.b_ref = b.id ORDER BY a.id, b.id"
	result, err := executeStatement(executor, query)
	if err != nil {
		t.Fatalf("mixed inner/left join: %v", err)
	}
	if want := [][]string{{"1", "10", "first"}, {"2", "20", "NULL"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("mixed inner/left rows = %#v, want %#v", result.rows, want)
	}

	result, err = executeStatement(executor, "SELECT a.id, b.id, c.label FROM issue216_a a LEFT JOIN issue216_b b ON b.a_ref = a.id LEFT JOIN issue216_c c ON c.b_ref = b.id ORDER BY a.id, b.id")
	if err != nil {
		t.Fatalf("left/left join: %v", err)
	}
	if want := [][]string{{"1", "10", "first"}, {"2", "20", "NULL"}, {"3", "NULL", "NULL"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("left/left rows = %#v, want %#v", result.rows, want)
	}
}
