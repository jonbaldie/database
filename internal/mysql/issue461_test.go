package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIssue461DropDatabaseClearsCanonicallyEquivalentSelection(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE DATABASE `straße`",
		"USE `straße`",
		"DROP DATABASE `STRASSE`",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT DATABASE()")
	if err != nil || len(result.rows) != 1 || len(result.nulls) != 1 || !result.nulls[0][0] {
		t.Fatalf("SELECT DATABASE() after drop = %#v, err = %v, want NULL", result, err)
	}
}

func TestIssue461RenameTableAcceptsCanonicallyEquivalentNamespace(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE DATABASE `straße`",
		"CREATE TABLE `straße`.t (id INT)",
		"INSERT INTO `straße`.t VALUES (1)",
		"RENAME TABLE `straße`.t TO `STRASSE`.u",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM `straße`.u")
	if err != nil || len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("SELECT id after rename = %#v, err = %v, want row 1", result, err)
	}
}

func TestIssue461ShowNameOrderUsesCanonicalKey(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE DATABASE `ß`",
		"CREATE DATABASE t",
		"CREATE TABLE `ß` (id INT)",
		"CREATE TABLE t (id INT)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	definition := executor.server.config.Catalog.Snapshot()
	namespaces := sortedNamespaces(definition)
	if len(namespaces) != 3 || namespaces[0].Name != "app" || namespaces[1].Name != "ß" || namespaces[2].Name != "t" {
		t.Fatalf("sorted namespaces = %#v, want app, ß, t", namespaces)
	}
	tables, err := executeStatement(executor, "SHOW TABLES")
	if err != nil || !equalRows(tables.rows, [][]string{{"ß"}, {"t"}}) {
		t.Fatalf("SHOW TABLES = %#v, err = %v", tables, err)
	}
}

func TestIssue461InformationSchemaUsesCanonicalIdentifiers(t *testing.T) {
	executor := ddlExecutorForTest(t)
	result, err := executeStatement(executor, "SELECT TABLE_ſCHEMA FROM `information_ſchema`.`TABLEſ`")
	if err != nil || len(result.rows) == 0 || len(result.columns) != 1 || result.columns[0] != "TABLE_SCHEMA" {
		t.Fatalf("canonical information_schema query = %#v, err = %v", result, err)
	}
}

func TestIssue461ShowIndexFallbackPreservesIdentifierSpelling(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE `CamelCase` (id INT PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := executor.server.config.Catalog.ApplyDurable(func(definition catalog.Definition) (catalog.Definition, error) {
		namespace := definition.Namespaces[catalog.Key("app")]
		table := namespace.Tables[catalog.Key("CamelCase")]
		table.Name = ""
		namespace.Tables[catalog.Key("CamelCase")] = table
		definition.Namespaces[catalog.Key("app")] = namespace
		return definition, nil
	}); err != nil {
		t.Fatalf("clear stored table name: %v", err)
	}

	result, err := executeStatement(executor, "SHOW INDEX FROM `CamelCase`")
	if err != nil || len(result.rows) != 1 || result.rows[0][0] != "CamelCase" {
		t.Fatalf("SHOW INDEX fallback = %#v, err = %v, want CamelCase", result, err)
	}
}
