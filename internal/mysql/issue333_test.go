package mysql

import (
	"reflect"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

// Issue 333: renaming a column left stale column names behind in
// table.Constraints, so a rename of a PRIMARY KEY or UNIQUE column failed
// validation with MySQL error 1072.
func TestIssue333RenameConstraintColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, val INT UNIQUE)",
		"INSERT INTO t VALUES (1, 10), (2, 20)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	if _, err := executeStatement(executor, "ALTER TABLE t RENAME COLUMN id TO new_id"); err != nil {
		t.Fatalf("rename primary key column: %v", err)
	}
	if _, err := executeStatement(executor, "ALTER TABLE t CHANGE COLUMN val new_val INT"); err != nil {
		t.Fatalf("change unique column: %v", err)
	}

	result, err := executeStatement(executor, "SELECT new_id, new_val FROM t ORDER BY new_id")
	if err != nil {
		t.Fatalf("select renamed columns: %v", err)
	}
	if want := [][]string{{"1", "10"}, {"2", "20"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}

	if _, err := executeStatement(executor, "INSERT INTO t VALUES (1, 30)"); err == nil {
		t.Fatal("duplicate primary key insert succeeded after rename")
	}
	if _, err := executeStatement(executor, "INSERT INTO t VALUES (3, 10)"); err == nil {
		t.Fatal("duplicate unique value insert succeeded after rename")
	}
	if _, err := executeStatement(executor, "INSERT INTO t VALUES (3, 30)"); err != nil {
		t.Fatalf("insert after rename: %v", err)
	}
}

// Renaming a parent or child column must keep foreign key metadata pointing at
// live columns on both sides, including on an empty table where the old
// shape check skipped constraint re-validation entirely.
func TestIssue333RenameForeignKeyColumns(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE parent (id INT PRIMARY KEY)",
		"CREATE TABLE child (id INT PRIMARY KEY, parent_id INT, CONSTRAINT child_parent FOREIGN KEY (parent_id) REFERENCES parent (id))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	if _, err := executeStatement(executor, "ALTER TABLE child RENAME COLUMN parent_id TO owner_id"); err != nil {
		t.Fatalf("rename foreign key column: %v", err)
	}
	if _, err := executeStatement(executor, "ALTER TABLE parent RENAME COLUMN id TO parent_key"); err != nil {
		t.Fatalf("rename referenced column: %v", err)
	}

	definition := executor.session.currentDefinition()
	child := definition.Namespaces[catalog.Key(executor.session.database)].Tables[catalog.Key("child")]
	for _, constraint := range child.Constraints {
		if constraint.Type != catalog.ConstraintTypeForeignKey {
			continue
		}
		if !reflect.DeepEqual(constraint.Columns, []string{"owner_id"}) {
			t.Errorf("foreign key columns = %#v, want [owner_id]", constraint.Columns)
		}
		if !reflect.DeepEqual(constraint.ReferencedColumns, []string{"parent_key"}) {
			t.Errorf("foreign key referenced columns = %#v, want [parent_key]", constraint.ReferencedColumns)
		}
	}

	for _, query := range []string{
		"INSERT INTO parent VALUES (1)",
		"INSERT INTO child VALUES (1, 1)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "INSERT INTO child VALUES (2, 99)"); err == nil {
		t.Fatal("insert violating the renamed foreign key succeeded")
	}
}

// sameTableShape must notice a rename on a table with no rows, which is the
// path that used to publish constraints naming a dropped column.
func TestIssue333EmptyTableRenameIsRevalidated(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE t (id INT PRIMARY KEY, val INT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := executeStatement(executor, "ALTER TABLE t RENAME COLUMN val TO amount"); err != nil {
		t.Fatalf("rename column on empty table: %v", err)
	}

	definition := executor.session.currentDefinition()
	table := definition.Namespaces[catalog.Key(executor.session.database)].Tables[catalog.Key("t")]
	if !reflect.DeepEqual(table.Columns, []string{"id", "amount"}) {
		t.Fatalf("columns = %#v, want [id amount]", table.Columns)
	}
	if sameTableShape(catalog.Table{Columns: []string{"id", "val"}}, catalog.Table{Columns: []string{"id", "amount"}}) {
		t.Error("sameTableShape treated a renamed column as an unchanged shape")
	}

	if _, err := executeStatement(executor, "INSERT INTO t VALUES (1, 5)"); err != nil {
		t.Fatalf("insert after rename: %v", err)
	}
}
