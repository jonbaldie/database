package mysql

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIssue384RenameTableUpdatesForeignKeyReferences(t *testing.T) {
	for _, test := range []struct {
		name   string
		rename string
	}{
		{name: "RENAME TABLE", rename: "RENAME TABLE parent TO parent_renamed"},
		{name: "ALTER TABLE RENAME", rename: "ALTER TABLE parent RENAME parent_renamed"},
		{name: "ALTER TABLE RENAME TO", rename: "ALTER TABLE parent RENAME TO parent_renamed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range []string{
				"CREATE TABLE parent (id INT PRIMARY KEY)",
				"CREATE TABLE child (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES parent(id))",
				"INSERT INTO parent VALUES (1)",
			} {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("execute %q: %v", query, err)
				}
			}

			if _, err := executeStatement(executor, test.rename); err != nil {
				t.Fatalf("rename parent table: %v", err)
			}
			if _, err := executeStatement(executor, "INSERT INTO child VALUES (1, 1)"); err != nil {
				t.Fatalf("insert through renamed foreign key: %v", err)
			}

			definition := executor.session.currentDefinition()
			child := definition.Namespaces[catalog.Key(executor.session.database)].Tables[catalog.Key("child")]
			var foreignKey catalog.Constraint
			for _, constraint := range child.Constraints {
				if constraint.Type == catalog.ConstraintTypeForeignKey {
					foreignKey = constraint
					break
				}
			}
			if foreignKey.ReferencedTable != "parent_renamed" {
				t.Fatalf("foreign key referenced table = %q, want parent_renamed", foreignKey.ReferencedTable)
			}

			result, err := executeStatement(executor, "SHOW CREATE TABLE child")
			if err != nil {
				t.Fatalf("show child definition: %v", err)
			}
			if len(result.rows) != 1 || !strings.Contains(result.rows[0][1], "REFERENCES `parent_renamed` (`id`)") {
				t.Fatalf("child definition = %#v, want renamed reference", result.rows)
			}
		})
	}
}

func TestIssue384RenameTableUpdatesSelfAndCrossNamespaceReferences(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE DATABASE child_db",
		"CREATE TABLE parent (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_self FOREIGN KEY (parent_id) REFERENCES parent(id))",
		"CREATE TABLE child_db.child (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES app.parent(id))",
		"INSERT INTO parent VALUES (1, NULL)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	if _, err := executeStatement(executor, "RENAME TABLE parent TO parent_renamed"); err != nil {
		t.Fatalf("rename parent table: %v", err)
	}
	for _, query := range []string{
		"INSERT INTO parent_renamed VALUES (2, 1)",
		"INSERT INTO child_db.child VALUES (10, 1)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("insert through renamed foreign key %q: %v", query, err)
		}
	}

	definition := executor.session.currentDefinition()
	for _, test := range []struct {
		namespace  string
		table      string
		constraint string
	}{
		{namespace: "app", table: "parent_renamed", constraint: "fk_self"},
		{namespace: "child_db", table: "child", constraint: "fk_parent"},
	} {
		table := definition.Namespaces[catalog.Key(test.namespace)].Tables[catalog.Key(test.table)]
		var foreignKey catalog.Constraint
		for _, constraint := range table.Constraints {
			if constraint.Name == test.constraint {
				foreignKey = constraint
				break
			}
		}
		if foreignKey.ReferencedTable != "parent_renamed" {
			t.Errorf("%s.%s constraint %s references %q, want parent_renamed", test.namespace, test.table, test.constraint, foreignKey.ReferencedTable)
		}
	}
}
