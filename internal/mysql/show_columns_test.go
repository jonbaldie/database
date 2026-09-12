package mysql

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

// showColumnsSeam confirms the statement executor is the seam under test.
// DESCRIBE, SHOW COLUMNS, and SHOW STATUS flow through the same policy entry
// as the MySQL wire protocol.
func TestDescribeReportsColumnStructure(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE sensors (id INT PRIMARY KEY AUTO_INCREMENT, site VARCHAR(64) NOT NULL, reading DECIMAL(10,2) DEFAULT 0, notes TEXT)",
		"INSERT INTO sensors (site, reading) VALUES ('harbor', 4.25)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "DESCRIBE sensors")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	wantColumns := []string{"Field", "Type", "Null", "Key", "Default", "Extra"}
	if !equalSlices(result.columns, wantColumns) {
		t.Fatalf("describe columns = %#v, want %#v", result.columns, wantColumns)
	}
	wantRows := [][]string{
		{"id", "INT", "NO", "PRI", "", "auto_increment"},
		{"site", "VARCHAR(64)", "NO", "", "", ""},
		{"reading", "DECIMAL(10,2)", "YES", "", "0.00", ""},
		{"notes", "TEXT", "YES", "", "", ""},
	}
	if !equalRows(result.rows, wantRows) {
		t.Fatalf("describe rows = %#v, want %#v", result.rows, wantRows)
	}
	wantNulls := [][]bool{
		{false, false, false, false, true, false},
		{false, false, false, false, true, false},
		{false, false, false, false, false, false},
		{false, false, false, false, true, false},
	}
	if !equalSlices(result.nulls, wantNulls) {
		t.Fatalf("describe nulls = %#v, want %#v", result.nulls, wantNulls)
	}
}

func equalSlices(left, right any) bool {
	return reflect.DeepEqual(left, right)
}

func TestShowColumnsMatchesDescribeShape(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE readings (id INT PRIMARY KEY AUTO_INCREMENT, site VARCHAR(64) NOT NULL, probe VARCHAR(32) UNIQUE, tag VARCHAR(8) NOT NULL UNIQUE, value INT, INDEX value_index (value))",
		"CREATE DATABASE archive",
		"CREATE TABLE archive.readings (id INT)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SHOW COLUMNS FROM readings")
	if err != nil {
		t.Fatalf("show columns: %v", err)
	}
	wantRows := [][]string{
		{"id", "INT", "NO", "PRI", "", "auto_increment"},
		{"site", "VARCHAR(64)", "NO", "", "", ""},
		{"probe", "VARCHAR(32)", "YES", "MUL", "", ""},
		{"tag", "VARCHAR(8)", "NO", "UNI", "", ""},
		{"value", "INT", "YES", "MUL", "", ""},
	}
	if !equalRows(result.rows, wantRows) {
		t.Fatalf("show columns rows = %#v, want %#v", result.rows, wantRows)
	}

	for _, query := range []string{"SHOW COLUMNS IN readings", "SHOW FIELDS FROM readings", "SHOW COLUMNS FROM app.readings"} {
		variant, err := executeStatement(executor, query)
		if err != nil || !equalRows(variant.rows, wantRows) {
			t.Fatalf("%s = %#v, err = %v", query, variant, err)
		}
	}

	like, err := executeStatement(executor, "SHOW COLUMNS FROM readings LIKE 'va%'")
	if err != nil || !equalRows(like.rows, [][]string{{"value", "INT", "YES", "MUL", "", ""}}) {
		t.Fatalf("show columns like = %#v, err = %v", like, err)
	}

	full, err := executeStatement(executor, "SHOW FULL COLUMNS FROM readings")
	if err != nil {
		t.Fatalf("show full columns: %v", err)
	}
	wantFullColumns := []string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}
	if !equalSlices(full.columns, wantFullColumns) {
		t.Fatalf("full columns = %#v, want %#v", full.columns, wantFullColumns)
	}
	wantFullRows := [][]string{
		{"id", "INT", "", "NO", "PRI", "", "auto_increment", "select,insert,update,references", ""},
		{"site", "VARCHAR(64)", "", "NO", "", "", "", "select,insert,update,references", ""},
		{"probe", "VARCHAR(32)", "", "YES", "MUL", "", "", "select,insert,update,references", ""},
		{"tag", "VARCHAR(8)", "", "NO", "UNI", "", "", "select,insert,update,references", ""},
		{"value", "INT", "", "YES", "MUL", "", "", "select,insert,update,references", ""},
	}
	wantFullNulls := [][]bool{
		{false, false, true, false, false, true, false, false, false},
		{false, false, true, false, false, true, false, false, false},
		{false, false, true, false, false, true, false, false, false},
		{false, false, true, false, false, true, false, false, false},
		{false, false, true, false, false, true, false, false, false},
	}
	if !equalRows(full.rows, wantFullRows) || !equalSlices(full.nulls, wantFullNulls) {
		t.Fatalf("full rows = %#v nulls = %#v", full.rows, full.nulls)
	}

	archived, err := executeStatement(executor, "DESCRIBE archive.readings")
	if err != nil || !equalRows(archived.rows, [][]string{{"id", "INT", "YES", "", "", ""}}) {
		t.Fatalf("describe qualified = %#v, err = %v", archived, err)
	}
}

func TestShowStatusReportsServerCounters(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"SHOW STATUS", "SHOW SESSION STATUS", "SHOW GLOBAL STATUS"} {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if !equalSlices(result.columns, []string{"Variable_name", "Value"}) {
			t.Fatalf("%s columns = %#v", query, result.columns)
		}
		if len(result.rows) == 0 {
			t.Fatalf("%s rows = %#v, want counters", query, result.rows)
		}
		for index := 1; index < len(result.rows); index++ {
			if result.rows[index-1][0] >= result.rows[index][0] {
				t.Fatalf("%s counters not sorted at %d: %#v", query, index, result.rows)
			}
		}
	}
	spills, err := executeStatement(executor, "SHOW STATUS LIKE '%spill%'")
	if err != nil || !equalRows(spills.rows, [][]string{{"spill_bytes", "0"}, {"spill_count", "0"}}) {
		t.Fatalf("show status like = %#v, err = %v", spills, err)
	}
}

func TestShowFullColumnsProjectsAccountGrants(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if err := executor.server.config.Catalog.CreateAccount(catalog.Account{
		Name:         "reader",
		PasswordHash: "hash",
		Grants:       []catalog.Grant{{Privilege: "DATA_READ", Namespace: "app"}},
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := executeStatement(executor, "CREATE TABLE devices (id INT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	executor.session.username = "reader"
	result, err := executeStatement(executor, "SHOW FULL COLUMNS FROM devices")
	if err != nil {
		t.Fatalf("show full columns: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][7] != "select" {
		t.Fatalf("reader privileges = %#v, want select only", result.rows)
	}
}

func TestShowStatusPrefixesStayExact(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{"SHOW SESSION STATUSX", "SHOW GLOBAL STATUSX", "SHOW STATUSX"} {
		if _, err := executeStatement(executor, query); err == nil || !strings.Contains(err.Error(), "unsupported query") {
			t.Fatalf("%s err = %v, want unsupported query", query, err)
		}
	}
}

func TestShowStatusReportsResourceUsage(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE records (id INT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	result, err := executeStatement(executor, "SHOW STATUS LIKE 'spill_count'")
	if err != nil {
		t.Fatalf("show status: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"spill_count", "0"}}) {
		t.Fatalf("spill count = %#v", result.rows)
	}
}

func TestShowColumnsAndDescribeFailures(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for query, want := range map[string]string{
		"DESCRIBE missing":  "table 'app.missing' doesn't exist",
		"SHOW COLUMNS FROM": "invalid table name",
		"DESCRIBE":          "invalid table name",
	} {
		if _, err := executeStatement(executor, query); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s err = %v, want %q", query, err, want)
		}
	}
}
