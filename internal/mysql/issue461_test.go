package mysql

import "testing"

func TestIssue461IdentifierEquivalentNamespaces(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		executor := ddlExecutorForTest(t)
		for _, q := range []string{"CREATE DATABASE `straße`", "CREATE TABLE `straße`.t (id INT)", "RENAME TABLE `straße`.t TO `STRASSE`.u"} {
			if _, err := executeStatement(executor, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	})
	t.Run("drop", func(t *testing.T) {
		executor := ddlExecutorForTest(t)
		for _, q := range []string{"CREATE DATABASE `straße`", "USE `straße`", "DROP DATABASE `STRASSE`"} {
			if _, err := executeStatement(executor, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		result, err := executeStatement(executor, "SELECT DATABASE()")
		if err != nil || len(result.nulls) != 1 || !result.nulls[0][0] {
			t.Fatalf("SELECT DATABASE() = %#v, err = %v; want NULL", result, err)
		}
	})
}
