package blackbox_test

import (
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLShowWarningsAndErrorsAreWireVisible(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "warnings-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, address, "admin", "warnings-secret")
	defer client.close()
	for _, query := range []string{
		"SHOW WARNINGS",
		"CREATE DATABASE warnings_app",
		"USE warnings_app",
		"CREATE TABLE entries (id INT)",
	} {
		result := client.query(query)
		if result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
		if query == "SHOW WARNINGS" {
			if len(result.columns) != 3 || result.columns[0] != "Level" || result.columns[1] != "Code" || result.columns[2] != "Message" || len(result.rows) != 0 {
				t.Fatalf("empty SHOW WARNINGS result = %#v", result)
			}
		}
	}

	warning := client.query("CREATE TABLE IF NOT EXISTS entries (id INT)")
	if warning.err != "" || warning.affected != 0 || warning.warnings != 1 {
		t.Fatalf("duplicate CREATE TABLE result = %#v", warning)
	}
	warnings := client.query("SHOW WARNINGS")
	assertDiagnosticRows(t, warnings, [][]string{{"Note", "1050", "Table 'warnings_app.entries' already exists"}})

	if errors := client.query("SHOW ERRORS"); errors.err != "" || len(errors.rows) != 0 {
		t.Fatalf("SHOW ERRORS after note = %#v", errors)
	}

	for _, query := range []string{
		"CREATE TABLE IF NOT EXISTS entries (id INT)",
		"SHOW WARNINGS LIMIT 1",
		"CREATE TABLE IF NOT EXISTS entries (id INT)",
		"SHOW WARNINGS LIMIT 1, 1",
		"CREATE TABLE IF NOT EXISTS entries (id INT)",
		"SHOW WARNINGS LIMIT 1 OFFSET 1",
	} {
		result := client.query(query)
		if result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
		if query == "SHOW WARNINGS LIMIT 1" {
			assertDiagnosticRows(t, result, [][]string{{"Note", "1050", "Table 'warnings_app.entries' already exists"}})
		}
		if (query == "SHOW WARNINGS LIMIT 1, 1" || query == "SHOW WARNINGS LIMIT 1 OFFSET 1") && len(result.rows) != 0 {
			t.Fatalf("offset beyond diagnostics = %#v", result.rows)
		}
	}

	failed := client.query("SELECT * FROM no_such_table")
	if failed.err == "" || failed.errCode != 1146 {
		t.Fatalf("expected wire statement error = %#v", failed)
	}
	errors := client.query("SHOW ERRORS LIMIT 1")
	assertDiagnosticRows(t, errors, [][]string{{"Error", "1146", "table does not exist"}})
	retained := client.query("SHOW WARNINGS")
	assertDiagnosticRows(t, retained, [][]string{{"Error", "1146", "table does not exist"}})
	if next := client.query("SELECT 1"); next.err != "" {
		t.Fatalf("next statement: %#v", next)
	}
	empty := client.query("SHOW WARNINGS")
	assertDiagnosticRows(t, empty, nil)
}

func assertDiagnosticRows(t *testing.T, result wireResult, want [][]string) {
	t.Helper()
	if result.err != "" || len(result.columns) != 3 || result.columns[0] != "Level" || result.columns[1] != "Code" || result.columns[2] != "Message" {
		t.Fatalf("diagnostic result header = %#v", result)
	}
	if len(result.rows) != len(want) {
		t.Fatalf("diagnostic rows = %#v, want %#v", result.rows, want)
	}
	for index := range want {
		if len(result.rows[index]) != len(want[index]) {
			t.Fatalf("diagnostic row %d = %#v, want %#v", index, result.rows[index], want[index])
		}
		for column := range want[index] {
			if result.rows[index][column] != want[index][column] {
				t.Fatalf("diagnostic row %d = %#v, want %#v", index, result.rows[index], want[index])
			}
		}
	}
}
