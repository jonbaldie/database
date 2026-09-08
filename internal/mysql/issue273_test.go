package mysql

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestIssue273CrossJoinIndexedColumnComparison(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE dept (id INT PRIMARY KEY, name VARCHAR(20), code VARCHAR(10), KEY (code))",
		"CREATE TABLE emp (id INT PRIMARY KEY, name VARCHAR(20), dept_id INT, dept_code VARCHAR(10), manager_id INT, KEY (dept_code))",
		"CREATE TABLE project (id INT PRIMARY KEY, emp_id INT, title VARCHAR(20))",
		"INSERT INTO dept VALUES (1, 'Eng', 'E'), (2, 'Sales', 'S')",
		"INSERT INTO emp VALUES (10, 'Alice', 1, 'E', 20), (20, 'Bob', 2, 'S', NULL)",
		"INSERT INTO project VALUES (100, 10, 'Alpha'), (200, 20, 'Beta')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	t.Run("RightTableTargetLeftTableLiteral", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e.name FROM dept d, emp e WHERE e.dept_id = d.id ORDER BY e.name")
		if err != nil {
			t.Fatalf("cross join WHERE: %v", err)
		}
		want := [][]string{{"Alice"}, {"Bob"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("LeftTableTargetRightTableLiteral", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT d.name FROM dept d, emp e WHERE d.id = e.id ORDER BY d.name")
		if err != nil {
			t.Fatalf("cross join WHERE: %v", err)
		}
		if len(result.rows) != 0 {
			t.Fatalf("rows = %#v, want empty", result.rows)
		}
	})

	t.Run("WithoutTableAliases", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT emp.name FROM dept, emp WHERE emp.dept_id = dept.id ORDER BY emp.name")
		if err != nil {
			t.Fatalf("cross join without aliases: %v", err)
		}
		want := [][]string{{"Alice"}, {"Bob"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("SecondaryIndexComparison", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e.name FROM dept d, emp e WHERE d.code = e.dept_code ORDER BY e.name")
		if err != nil {
			t.Fatalf("secondary index comparison: %v", err)
		}
		want := [][]string{{"Alice"}, {"Bob"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("NonIndexedColumnComparison", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e.name FROM dept d, emp e WHERE e.name = d.name ORDER BY e.name")
		if err != nil {
			t.Fatalf("non-indexed column comparison: %v", err)
		}
		if len(result.rows) != 0 {
			t.Fatalf("rows = %#v, want empty", result.rows)
		}
	})

	t.Run("MultiplePredicatesWithLiteralBound", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e.name FROM dept d, emp e WHERE e.dept_id = d.id AND d.id = 1 ORDER BY e.name")
		if err != nil {
			t.Fatalf("multiple predicates: %v", err)
		}
		want := [][]string{{"Alice"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("MultiplePredicatesWithGreaterComparison", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e.name FROM dept d, emp e WHERE e.dept_id = d.id AND e.id > 15 ORDER BY e.name")
		if err != nil {
			t.Fatalf("multiple predicates with > comparison: %v", err)
		}
		want := [][]string{{"Bob"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("ThreeTableJoin", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT p.title, e.name, d.name FROM dept d, emp e, project p WHERE e.dept_id = d.id AND p.emp_id = e.id ORDER BY p.title")
		if err != nil {
			t.Fatalf("three table join: %v", err)
		}
		want := [][]string{{"Alpha", "Alice", "Eng"}, {"Beta", "Bob", "Sales"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("SelfJoin", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e1.name, e2.name FROM emp e1, emp e2 WHERE e1.manager_id = e2.id ORDER BY e1.name")
		if err != nil {
			t.Fatalf("self join: %v", err)
		}
		want := [][]string{{"Alice", "Bob"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("InnerJoinOnUnchanged", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e.name FROM emp e INNER JOIN dept d ON e.dept_id = d.id ORDER BY e.name")
		if err != nil {
			t.Fatalf("inner join ON: %v", err)
		}
		want := [][]string{{"Alice"}, {"Bob"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("ForceIndexOnCommaJoin", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT e.name FROM dept d FORCE INDEX (PRIMARY), emp e WHERE e.dept_id = d.id ORDER BY e.name")
		if err != nil {
			t.Fatalf("force index on comma join: %v", err)
		}
		want := [][]string{{"Alice"}, {"Bob"}}
		if !reflect.DeepEqual(result.rows, want) {
			t.Fatalf("rows = %#v, want %#v", result.rows, want)
		}
	})

	t.Run("ExplainFormatJSON", func(t *testing.T) {
		result, err := executeStatement(executor, "EXPLAIN FORMAT=JSON SELECT e.name FROM dept d, emp e WHERE e.dept_id = d.id")
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		if len(result.rows) != 1 || len(result.rows[0]) != 1 {
			t.Fatalf("unexpected explain result: %#v", result.rows)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(result.rows[0][0]), &parsed); err != nil {
			t.Fatalf("unmarshal explain json: %v", err)
		}
	})
}
