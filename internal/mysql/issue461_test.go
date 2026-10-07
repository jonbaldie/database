package mysql

import (
	"testing"
)

func TestIssue461DropDatabaseClearsCanonicallyEquivalentSelection(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"CREATE DATABASE `straße`", "USE `straße`", "DROP DATABASE `STRASSE`"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT DATABASE()")
	if err != nil || len(result.nulls) != 1 || len(result.nulls[0]) != 1 || !result.nulls[0][0] {
		t.Fatalf("SELECT DATABASE() after drop = %#v, err = %v, want NULL", result, err)
	}
}

func TestIssue461RenameTableAcceptsCanonicallyEquivalentNamespace(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE DATABASE `straße`",
		"CREATE TABLE `straße`.t (id INT)",
		"RENAME TABLE `straße`.t TO `STRASSE`.u",
		"CREATE DATABASE `é`",
		"CREATE TABLE `é`.t (id INT)",
		"RENAME TABLE `é`.t TO `É`.u",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%q: %v", query, err)
		}
	}
	for _, query := range []string{"SELECT * FROM `straße`.u", "SELECT * FROM `é`.u"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%q after rename: %v", query, err)
		}
	}
}
