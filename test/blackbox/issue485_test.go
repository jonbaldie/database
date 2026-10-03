package blackbox_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue485InformationSchemaColumnsThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pid INT, CONSTRAINT fk1 FOREIGN KEY (pid) REFERENCES p (id))",
		"CREATE INDEX ic ON c (pid)",
		"INSERT INTO c VALUES (1, NULL), (2, NULL)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	constraints := client.query("SELECT CONSTRAINT_CATALOG, ENFORCED FROM information_schema.TABLE_CONSTRAINTS WHERE TABLE_NAME = 'c'")
	if constraints.err != "" || strings.Join(constraints.columns, ",") != "CONSTRAINT_CATALOG,ENFORCED" || len(constraints.rows) != 2 {
		t.Fatalf("TABLE_CONSTRAINTS: %#v", constraints)
	}
	for _, row := range constraints.rows {
		if len(row) != 2 || row[0] != "def" || row[1] != "YES" {
			t.Fatalf("TABLE_CONSTRAINTS row: %#v", row)
		}
	}

	referential := client.query("SELECT UPDATE_RULE, DELETE_RULE FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE TABLE_NAME = 'c'")
	if referential.err != "" || strings.Join(referential.columns, ",") != "UPDATE_RULE,DELETE_RULE" || len(referential.rows) != 1 || strings.Join(referential.rows[0], ",") != "NO ACTION,NO ACTION" {
		t.Fatalf("REFERENTIAL_CONSTRAINTS: %#v", referential)
	}

	keyUsage := client.query("SELECT POSITION_IN_UNIQUE_CONSTRAINT FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_NAME = 'c'")
	if keyUsage.err != "" || strings.Join(keyUsage.columns, ",") != "POSITION_IN_UNIQUE_CONSTRAINT" || len(keyUsage.rows) != 2 {
		t.Fatalf("KEY_COLUMN_USAGE: %#v", keyUsage)
	}
	foundForeignKeyPosition, foundNullPrimaryPosition := false, false
	for index, row := range keyUsage.rows {
		if len(row) != 1 || index >= len(keyUsage.nulls) || len(keyUsage.nulls[index]) != 1 {
			t.Fatalf("KEY_COLUMN_USAGE row: %#v; nulls %#v", row, keyUsage.nulls)
		}
		if keyUsage.nulls[index][0] {
			foundNullPrimaryPosition = true
		} else if row[0] == "1" {
			foundForeignKeyPosition = true
		}
	}
	if !foundForeignKeyPosition || !foundNullPrimaryPosition {
		t.Fatalf("KEY_COLUMN_USAGE positions: %#v; nulls %#v", keyUsage.rows, keyUsage.nulls)
	}

	statistics := client.query("SELECT INDEX_NAME, CARDINALITY, IS_VISIBLE FROM information_schema.STATISTICS WHERE TABLE_NAME = 'c'")
	if statistics.err != "" || strings.Join(statistics.columns, ",") != "INDEX_NAME,CARDINALITY,IS_VISIBLE" || len(statistics.rows) != 2 {
		t.Fatalf("STATISTICS: %#v", statistics)
	}
	foundIndexes := make(map[string]bool, len(statistics.rows))
	for _, row := range statistics.rows {
		if len(row) != 3 {
			t.Fatalf("STATISTICS row: %#v", row)
		}
		cardinality, err := strconv.Atoi(row[1])
		if err != nil || cardinality < 0 || row[2] != "YES" {
			t.Fatalf("STATISTICS row: %#v", row)
		}
		foundIndexes[row[0]] = true
	}
	if !foundIndexes["ic"] || !foundIndexes["PRIMARY"] {
		t.Fatalf("STATISTICS indexes: %#v", statistics.rows)
	}
}
