package mysql

import "testing"

func TestShowWarningsAndErrorsReturnEmptyDiagnostics(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"SHOW WARNINGS",
		"SHOW   WARNINGS",
		"SHOW WARNINGS LIMIT 1",
		"SHOW WARNINGS LIMIT 1, 1",
		"SHOW WARNINGS LIMIT 1 OFFSET 1",
		"SHOW ERRORS",
		"SHOW\tERRORS",
	} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
		if result == nil || !equalRows(result.rows, nil) || !equalStrings(result.columns, []string{"Level", "Code", "Message"}) {
			t.Fatalf("diagnostics for %q = %#v, want empty Level/Code/Message result", query, result)
		}
	}
}

func TestSuccessfulNoOpsCreateRetrievableDiagnostics(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE DATABASE reports",
		"CREATE DATABASE IF NOT EXISTS reports",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SHOW WARNINGS")
	if err != nil {
		t.Fatalf("show database warning: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"Note", "1007", "Can't create database 'reports'; database exists"}}) {
		t.Fatalf("database diagnostics = %#v", result.rows)
	}

	for _, query := range []string{
		"CREATE TABLE entries (id INT)",
		"CREATE TABLE IF NOT EXISTS entries (id INT)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err = executeStatement(executor, "SHOW WARNINGS")
	if err != nil {
		t.Fatalf("show table warning: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"Note", "1050", "Table 'app.entries' already exists"}}) {
		t.Fatalf("table diagnostics = %#v", result.rows)
	}

	if _, err := executeStatement(executor, "DROP TABLE IF EXISTS absent"); err != nil {
		t.Fatalf("drop missing table: %v", err)
	}
	result, err = executeStatement(executor, "SHOW WARNINGS LIMIT 0, 1")
	if err != nil {
		t.Fatalf("show drop warning: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"Note", "1051", "Unknown table 'app.absent'"}}) {
		t.Fatalf("drop diagnostics = %#v", result.rows)
	}
}

func TestShowWarningsRetainsPreviousStatementDiagnosticsUntilNextStatement(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "unsupported statement"); err == nil {
		t.Fatal("unsupported statement succeeded")
	}
	result, err := executeStatement(executor, "SHOW WARNINGS")
	if err != nil {
		t.Fatalf("show previous diagnostic: %v", err)
	}
	want := [][]string{{"Error", "1064", "unsupported query: unsupported statement"}}
	if !equalRows(result.rows, want) {
		t.Fatalf("diagnostics = %#v, want %#v", result.rows, want)
	}
	result, err = executeStatement(executor, "SHOW ERRORS")
	if err != nil {
		t.Fatalf("show errors after SHOW WARNINGS: %v", err)
	}
	if !equalRows(result.rows, want) {
		t.Fatalf("diagnostics after SHOW WARNINGS = %#v, want %#v", result.rows, want)
	}
	if _, err := executeStatement(executor, "SELECT 1"); err != nil {
		t.Fatalf("clear diagnostics with next statement: %v", err)
	}
	result, err = executeStatement(executor, "SHOW WARNINGS")
	if err != nil {
		t.Fatalf("show diagnostics after next statement: %v", err)
	}
	if len(result.rows) != 0 {
		t.Fatalf("diagnostics after next statement = %#v, want empty", result.rows)
	}
}

func TestShowErrorsFiltersSessionDiagnostics(t *testing.T) {
	executor := ddlExecutorForTest(t)
	executor.session.addDiagnostic("Note", 1265, "rounded")
	executor.session.addDiagnostic("Warning", 1000, "warning")
	executor.session.addDiagnostic("Error", 1064, "error")
	result, err := executeStatement(executor, "SHOW ERRORS LIMIT 0, 1")
	if err != nil {
		t.Fatalf("show errors: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"Error", "1064", "error"}}) {
		t.Fatalf("errors = %#v", result.rows)
	}
}
