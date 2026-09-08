package mysql

import (
	"reflect"
	"testing"
)

func TestIssue274GroupByPrimaryKeyAllowsDependentColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE dept (id INT PRIMARY KEY, name VARCHAR(20))",
		"INSERT INTO dept VALUES (1, 'Eng'), (2, 'Sales')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT id, name FROM dept GROUP BY id ORDER BY id")
	if err != nil {
		t.Fatalf("GROUP BY primary key: %v", err)
	}
	want := [][]string{{"1", "Eng"}, {"2", "Sales"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}

	star, err := executeStatement(executor, "SELECT * FROM dept GROUP BY id ORDER BY id")
	if err != nil || !reflect.DeepEqual(star.rows, want) {
		t.Fatalf("SELECT * GROUP BY primary key: rows = %#v, err = %v", star, err)
	}

	dependent, err := executeStatement(executor, "SELECT name FROM dept GROUP BY id ORDER BY id")
	if err != nil || !reflect.DeepEqual(dependent.rows, [][]string{{"Eng"}, {"Sales"}}) {
		t.Fatalf("dependent column only: rows = %#v, err = %v", dependent, err)
	}

	if _, err := executeStatement(executor, "SELECT id, name FROM dept GROUP BY name"); !isFailureCode(err, 1055) {
		t.Fatalf("GROUP BY non-key column: got %v, want 1055", err)
	}
}

func TestIssue274GroupByPrimaryKeyAllowsDependentJoinColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE dept (id INT PRIMARY KEY, name VARCHAR(20))",
		"CREATE TABLE emp (id INT PRIMARY KEY, dept_id INT, salary INT)",
		"INSERT INTO dept VALUES (1, 'Eng'), (2, 'Sales')",
		"INSERT INTO emp VALUES (10, 1, 100), (11, 1, 200), (12, 2, 150)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT d.name, SUM(e.salary) FROM dept d INNER JOIN emp e ON d.id = e.dept_id GROUP BY d.id ORDER BY d.id")
	if err != nil {
		t.Fatalf("GROUP BY join primary key: %v", err)
	}
	want := [][]string{{"Eng", "300"}, {"Sales", "150"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}

	if _, err := executeStatement(executor, "SELECT e.salary FROM dept d LEFT JOIN emp e ON d.id = e.dept_id GROUP BY e.id"); !isFailureCode(err, 1055) {
		t.Fatalf("LEFT JOIN inner primary key: got %v, want 1055", err)
	}
}

func TestIssue274GroupByUniqueKeyAllowsDependentColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE dept (id INT PRIMARY KEY, code VARCHAR(20) NOT NULL UNIQUE, name VARCHAR(20))",
		"INSERT INTO dept VALUES (1, 'ENG', 'Eng'), (2, 'SAL', 'Sales')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT id, name FROM dept GROUP BY code ORDER BY code")
	if err != nil {
		t.Fatalf("GROUP BY unique key: %v", err)
	}
	want := [][]string{{"1", "Eng"}, {"2", "Sales"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}
}

func TestIssue274GroupByNullableUniqueKeyRejectsDependentColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE dept (id INT PRIMARY KEY, code VARCHAR(20) UNIQUE, name VARCHAR(20))",
		"INSERT INTO dept VALUES (1, 'ENG', 'Eng'), (2, 'SAL', 'Sales')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	if _, err := executeStatement(executor, "SELECT id, name FROM dept GROUP BY code"); !isFailureCode(err, 1055) {
		t.Fatalf("GROUP BY nullable unique key: got %v, want 1055", err)
	}
}

func TestIssue274GroupByCompositePrimaryKeyRequiresAllKeyColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE dept (org INT, id INT, name VARCHAR(20), PRIMARY KEY (org, id))",
		"INSERT INTO dept VALUES (1, 1, 'Eng'), (1, 2, 'Sales')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	if _, err := executeStatement(executor, "SELECT name FROM dept GROUP BY org"); !isFailureCode(err, 1055) {
		t.Fatalf("incomplete composite primary key: got %v, want 1055", err)
	}

	result, err := executeStatement(executor, "SELECT name FROM dept GROUP BY org, id ORDER BY org, id")
	if err != nil || !reflect.DeepEqual(result.rows, [][]string{{"Eng"}, {"Sales"}}) {
		t.Fatalf("complete composite primary key: rows = %#v, err = %v", result, err)
	}
}
