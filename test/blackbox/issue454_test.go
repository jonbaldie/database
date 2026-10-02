package blackbox_test

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue454ShowIndexMatchesInformationSchemaNullability(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE metadata_test",
		"USE metadata_test",
		"CREATE TABLE records (id INT PRIMARY KEY, label INT, INDEX label_index (label))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	show := client.query("SHOW INDEX FROM records")
	if show.err != "" {
		t.Fatalf("SHOW INDEX FROM records: %#v", show)
	}
	statistics := client.query("SELECT INDEX_NAME, SEQ_IN_INDEX, COLUMN_NAME, NULLABLE FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = 'metadata_test' AND TABLE_NAME = 'records' ORDER BY INDEX_NAME, SEQ_IN_INDEX")
	if statistics.err != "" {
		t.Fatalf("information_schema.STATISTICS: %#v", statistics)
	}

	for _, expected := range []struct {
		name     string
		column   string
		nullable string
	}{
		{name: "PRIMARY", column: "id", nullable: ""},
		{name: "label_index", column: "label", nullable: "YES"},
	} {
		showRow := wireMetadataRow(t, show, "Key_name", expected.name)
		showName := wireMetadataColumn(t, show, "Null")
		if show.nulls[showRow][showName] || show.rows[showRow][showName] != expected.nullable {
			t.Errorf("SHOW INDEX %s nullability = (%q, NULL=%t), want %q", expected.name, show.rows[showRow][showName], show.nulls[showRow][showName], expected.nullable)
		}

		statisticsRow := wireMetadataRow(t, statistics, "INDEX_NAME", expected.name)
		if statistics.rows[statisticsRow][wireMetadataColumn(t, statistics, "COLUMN_NAME")] != expected.column ||
			statistics.rows[statisticsRow][wireMetadataColumn(t, statistics, "NULLABLE")] != expected.nullable {
			t.Errorf("information_schema.STATISTICS %s row = %#v, want column %q and nullable %q", expected.name, statistics.rows[statisticsRow], expected.column, expected.nullable)
		}
	}
}

func TestIssue454MetadataSurfacesShareCatalogFacts(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE catalog_projection",
		"USE catalog_projection",
		"CREATE TABLE parents (id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(12) NOT NULL UNIQUE)",
		"CREATE TABLE children (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_children_parent FOREIGN KEY (parent_id) REFERENCES parents(id), CONSTRAINT chk_children_id CHECK (id > 0))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	databases := client.query("SHOW DATABASES")
	schemata := client.query("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME")
	if databases.err != "" || schemata.err != "" || !equalStringRows(databases.rows, schemata.rows) {
		t.Fatalf("SHOW DATABASES = %#v, SCHEMATA = %#v", databases, schemata)
	}

	showTables := client.query("SHOW FULL TABLES FROM catalog_projection")
	catalogTables := client.query("SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'catalog_projection' ORDER BY TABLE_NAME")
	wantTables := [][]string{{"children", "BASE TABLE"}, {"parents", "BASE TABLE"}}
	if showTables.err != "" || catalogTables.err != "" || !equalStringRows(showTables.rows, wantTables) || !equalStringRows(catalogTables.rows, wantTables) {
		t.Fatalf("SHOW FULL TABLES = %#v, TABLES = %#v", showTables, catalogTables)
	}

	showColumns := client.query("SHOW COLUMNS FROM parents")
	catalogColumns := client.query("SELECT COLUMN_NAME, COLUMN_TYPE, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'catalog_projection' AND TABLE_NAME = 'parents' ORDER BY ORDINAL_POSITION")
	if showColumns.err != "" || catalogColumns.err != "" || len(showColumns.rows) != len(catalogColumns.rows) {
		t.Fatalf("SHOW COLUMNS = %#v, COLUMNS = %#v", showColumns, catalogColumns)
	}
	for index, row := range showColumns.rows {
		if row[0] != catalogColumns.rows[index][0] || row[1] != catalogColumns.rows[index][1] || row[5] != catalogColumns.rows[index][2] {
			t.Errorf("column metadata differs at position %d: SHOW=%#v COLUMNS=%#v", index+1, row, catalogColumns.rows[index])
		}
	}

	constraints := client.query("SELECT CONSTRAINT_NAME, CONSTRAINT_TYPE FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_SCHEMA = 'catalog_projection' AND TABLE_NAME = 'children' ORDER BY CONSTRAINT_NAME")
	wantConstraints := [][]string{{"chk_children_id", "CHECK"}, {"fk_children_parent", "FOREIGN KEY"}, {"PRIMARY", "PRIMARY KEY"}}
	if constraints.err != "" || !equalStringRows(constraints.rows, wantConstraints) {
		t.Fatalf("TABLE_CONSTRAINTS = %#v", constraints)
	}

	keyUsage := client.query("SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA = 'catalog_projection' AND TABLE_NAME = 'children' ORDER BY CONSTRAINT_NAME")
	if keyUsage.err != "" || len(keyUsage.rows) != 2 {
		t.Fatalf("KEY_COLUMN_USAGE = %#v", keyUsage)
	}
	primaryKeyUsage := wireMetadataRow(t, keyUsage, "CONSTRAINT_NAME", "PRIMARY")
	foreignKeyUsage := wireMetadataRow(t, keyUsage, "CONSTRAINT_NAME", "fk_children_parent")
	if keyUsage.rows[primaryKeyUsage][1] != "id" ||
		!keyUsage.nulls[primaryKeyUsage][2] || !keyUsage.nulls[primaryKeyUsage][3] ||
		keyUsage.rows[foreignKeyUsage][1] != "parent_id" || keyUsage.rows[foreignKeyUsage][2] != "parents" || keyUsage.rows[foreignKeyUsage][3] != "id" {
		t.Fatalf("KEY_COLUMN_USAGE rows = %#v", keyUsage)
	}

	referential := client.query("SELECT CONSTRAINT_NAME, TABLE_NAME, REFERENCED_TABLE_NAME FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA = 'catalog_projection'")
	if referential.err != "" || !equalStringRows(referential.rows, [][]string{{"fk_children_parent", "children", "parents"}}) {
		t.Fatalf("REFERENTIAL_CONSTRAINTS = %#v", referential)
	}

	checks := client.query("SELECT CONSTRAINT_NAME, CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS WHERE CONSTRAINT_SCHEMA = 'catalog_projection'")
	if checks.err != "" || len(checks.rows) != 1 || checks.rows[0][0] != "chk_children_id" || checks.rows[0][1] == "" {
		t.Fatalf("CHECK_CONSTRAINTS = %#v", checks)
	}

	createTable := client.query("SHOW CREATE TABLE children")
	if createTable.err != "" || len(createTable.rows) != 1 || !strings.Contains(createTable.rows[0][1], "fk_children_parent") || !strings.Contains(createTable.rows[0][1], "chk_children_id") {
		t.Fatalf("SHOW CREATE TABLE = %#v", createTable)
	}

	for _, view := range []string{
		"SCHEMATA", "TABLES", "COLUMNS", "STATISTICS", "TABLE_CONSTRAINTS", "KEY_COLUMN_USAGE",
		"REFERENTIAL_CONSTRAINTS", "CHECK_CONSTRAINTS", "CHARACTER_SETS", "COLLATIONS",
		"ACCOUNTS", "ACCOUNT_GRANTS", "PROCESSLIST",
	} {
		result := client.query("SELECT * FROM information_schema." + view)
		if result.err != "" {
			t.Fatalf("SELECT * FROM information_schema.%s: %#v", view, result)
		}
		for index, row := range result.rows {
			if len(row) != len(result.columns) {
				t.Errorf("%s row %d has %d values for %d columns", view, index, len(row), len(result.columns))
			}
		}
	}
}

func wireMetadataColumn(t *testing.T, result wireResult, name string) int {
	t.Helper()
	for index, column := range result.columns {
		if column == name {
			return index
		}
	}
	t.Fatalf("query columns %#v do not include %q", result.columns, name)
	return -1
}

func wireMetadataRow(t *testing.T, result wireResult, column, value string) int {
	t.Helper()
	columnIndex := wireMetadataColumn(t, result, column)
	for index, row := range result.rows {
		if row[columnIndex] == value {
			return index
		}
	}
	t.Fatalf("query rows %#v do not include %s = %q", result.rows, column, value)
	return -1
}
