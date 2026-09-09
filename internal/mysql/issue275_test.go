package mysql

import (
	"errors"
	"testing"
)

func TestIssue275CastToDate(t *testing.T) {
	value, err := evaluateScalar("CAST('2024-01-15' AS DATE)")
	if err != nil {
		t.Fatalf("CAST AS DATE: %v", err)
	}
	if value.kind != valueString || value.s != "2024-01-15" || value.temporal != temporalDate {
		t.Fatalf("CAST AS DATE value = %#v, want temporal string 2024-01-15", value)
	}

	executor := expressionExecutor(t)
	result, err := executeStatement(executor, "SELECT CAST('2024-01-15' AS DATE)")
	if err != nil {
		t.Fatalf("SELECT CAST AS DATE: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "2024-01-15" {
		t.Fatalf("SELECT CAST AS DATE rows = %#v", result.rows)
	}
	metadata := result.metadata[0]
	if metadata.typ != mysqlTypeDate || metadata.length != 10 {
		t.Fatalf("SELECT CAST AS DATE metadata = %#v, want DATE wire type with length 10", metadata)
	}
}

func TestIssue275CastToDatetimeAndTime(t *testing.T) {
	executor := expressionExecutor(t)
	cases := map[string]struct {
		value    string
		metadata columnMetadata
	}{
		"SELECT CAST('2024-01-15 10:30:00' AS DATETIME)": {"2024-01-15 10:30:00", columnMetadata{typ: mysqlTypeDatetime, length: 19}},
		"SELECT CONVERT('2024-01-15', DATE)":             {"2024-01-15", columnMetadata{typ: mysqlTypeDate, length: 10}},
		"SELECT CAST('10:30:00' AS TIME)":                {"10:30:00", columnMetadata{typ: mysqlTypeTime, length: 10}},
		"SELECT CAST('2024-01-15 10:30:00.5' AS DATETIME(3))": {
			"2024-01-15 10:30:00.500",
			columnMetadata{typ: mysqlTypeDatetime, length: 23, decimals: 3},
		},
		"SELECT CAST('10:30:00.5' AS TIME(3))": {
			"10:30:00.500",
			columnMetadata{typ: mysqlTypeTime, length: 14, decimals: 3},
		},
	}
	for query, want := range cases {
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute(%q) error: %v", query, err)
		}
		if len(result.rows) != 1 || result.rows[0][0] != want.value {
			t.Errorf("execute(%q) rows = %#v, want %q", query, result.rows, want.value)
		}
		metadata := result.metadata[0]
		if metadata.typ != want.metadata.typ || metadata.length != want.metadata.length || metadata.decimals != want.metadata.decimals {
			t.Errorf("execute(%q) metadata = %#v, want %#v", query, metadata, want.metadata)
		}
	}
}

func TestIssue275CastToApproximateNumeric(t *testing.T) {
	cases := map[string]string{
		"CAST(3 AS DOUBLE)":                    "3",
		"CAST(3 AS REAL)":                      "3",
		"CAST(3 AS FLOAT)":                     "3",
		"CAST('2.5' AS DOUBLE)":                "2.5",
		"CAST(3.7 AS DOUBLE)":                  "3.7",
		"CAST(18446744073709551615 AS DOUBLE)": "18446744073709552000",
	}
	for expression, want := range cases {
		value, err := evaluateScalar(expression)
		if err != nil {
			t.Fatalf("evaluate(%q) error: %v", expression, err)
		}
		if value.kind != valueDouble || value.render() != want {
			t.Errorf("evaluate(%q) = %#v, want double %q", expression, value, want)
		}
	}

	value, err := evaluateScalar("CAST(0.123456789 AS FLOAT)")
	if err != nil {
		t.Fatalf("CAST AS FLOAT: %v", err)
	}
	if want := "0.12345679"; value.render() != want {
		t.Errorf("CAST(0.123456789 AS FLOAT) = %q, want single-precision %q", value.render(), want)
	}

	if _, err := evaluateScalar("CAST('abc' AS DOUBLE)"); err == nil {
		t.Fatalf("CAST('abc' AS DOUBLE) succeeded, want a cast error")
	}
}

func TestIssue275CastToBinary(t *testing.T) {
	cases := map[string]string{
		"CAST('abc' AS BINARY)":    "abc",
		"CAST(12 AS BINARY)":       "12",
		"CAST('abc' AS BINARY(3))": "abc",
		"CAST('a' AS BINARY(3))":   "a\x00\x00",
		"CAST('' AS BINARY)":       "",
	}
	for expression, want := range cases {
		value, err := evaluateScalar(expression)
		if err != nil {
			t.Fatalf("evaluate(%q) error: %v", expression, err)
		}
		if value.kind != valueString || value.s != want || !value.binary {
			t.Errorf("evaluate(%q) = %#v, want binary %q", expression, value, want)
		}
	}

	executor := expressionExecutor(t)
	result, err := executeStatement(executor, "SELECT CAST('abc' AS BINARY)")
	if err != nil {
		t.Fatalf("SELECT CAST AS BINARY: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "abc" {
		t.Fatalf("SELECT CAST AS BINARY rows = %#v", result.rows)
	}
	if metadata := result.metadata[0]; metadata.characterSet != mysqlCharsetBinary || metadata.typ != mysqlTypeVarString {
		t.Fatalf("SELECT CAST AS BINARY metadata = %#v, want binary charset var string", metadata)
	}

	if _, err := evaluateScalar("CAST('abcd' AS BINARY(3))"); err == nil {
		t.Fatalf("CAST('abcd' AS BINARY(3)) succeeded, want a cast error")
	}
}

func TestIssue275CastToYear(t *testing.T) {
	value, err := evaluateScalar("CAST('2024' AS YEAR)")
	if err != nil {
		t.Fatalf("CAST AS YEAR: %v", err)
	}
	if value.kind != valueString || value.s != "2024" || value.temporal != temporalYear {
		t.Fatalf("CAST AS YEAR value = %#v, want temporal string 2024", value)
	}

	executor := expressionExecutor(t)
	result, err := executeStatement(executor, "SELECT CAST('2024' AS YEAR)")
	if err != nil {
		t.Fatalf("SELECT CAST AS YEAR: %v", err)
	}
	if metadata := result.metadata[0]; metadata.typ != mysqlTypeYear || metadata.length != 4 {
		t.Fatalf("SELECT CAST AS YEAR metadata = %#v, want YEAR wire type", metadata)
	}
}

func TestIssue275CastBetweenTemporalFamilies(t *testing.T) {
	cases := map[string]string{
		"CAST(CAST('2024-01-15 10:30:00' AS DATETIME) AS DATE)":         "2024-01-15",
		"CAST(CAST('2024-01-15' AS DATE) AS DATETIME)":                  "2024-01-15 00:00:00",
		"CAST(CAST('2024-01-15 10:30:00' AS DATETIME) AS TIME)":         "10:30:00",
		"CAST(CAST('2024-01-15' AS DATE) AS TIME)":                      "00:00:00",
		"CAST(CAST('2024-01-15 10:30:00.5' AS DATETIME(1)) AS TIME(1))": "10:30:00.5",
	}
	for expression, want := range cases {
		value, err := evaluateScalar(expression)
		if err != nil {
			t.Fatalf("evaluate(%q) error: %v", expression, err)
		}
		if value.render() != want || value.temporal == temporalNone {
			t.Errorf("evaluate(%q) = %#v, want temporal %q", expression, value, want)
		}
	}
}

func TestIssue275CastsTableColumns(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	if err := store.CreateTableWithTypes("app", "issue275rel", []string{"id", "moment"}, []string{"INT", "DATETIME"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "issue275rel", []string{"1", "2024-01-15 10:30:00"}); err != nil {
		t.Fatal(err)
	}

	result, err := executeStatement(executor, "SELECT CAST(moment AS DATE) FROM issue275rel WHERE id = 1")
	if err != nil {
		t.Fatalf("SELECT CAST(column AS DATE): %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "2024-01-15" {
		t.Fatalf("SELECT CAST(column AS DATE) rows = %#v", result.rows)
	}
	if metadata := result.metadata[0]; metadata.typ != mysqlTypeDate || metadata.length != 10 {
		t.Fatalf("SELECT CAST(column AS DATE) metadata = %#v, want DATE wire type with length 10", metadata)
	}

	result, err = executeStatement(executor, "SELECT CAST('2024-01-15' AS DATE) FROM issue275rel WHERE id = 1")
	if err != nil {
		t.Fatalf("SELECT CAST(literal AS DATE) with FROM: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "2024-01-15" {
		t.Fatalf("SELECT CAST(literal AS DATE) with FROM rows = %#v", result.rows)
	}
}

func TestIssue275RejectsMalformedAndOutOfRangeCasts(t *testing.T) {
	cases := map[string]uint16{
		"CAST('abc' AS DATE)":               1292,
		"CAST('2024-02-30' AS DATE)":        1292,
		"CAST('abc' AS DATETIME)":           1292,
		"CAST('abc' AS TIME)":               1292,
		"CAST('abc' AS YEAR)":               1292,
		"CAST('999:00:00' AS TIME)":         1264,
		"CAST('99999-01-01' AS DATE)":       1292,
		"CAST('2024-01-15' AS DATETIME(9))": 1426,
		"CAST('nan' AS DOUBLE)":             1292,
		"CAST('1e400' AS DOUBLE)":           1690,
		"CAST(1e300 AS FLOAT)":              1690,
	}
	for expression, code := range cases {
		_, err := evaluateScalar(expression)
		var failure sqlFailure
		if !errors.As(err, &failure) || failure.code != code {
			t.Errorf("evaluate(%q) error = %v, want code %d", expression, err, code)
		}
	}
}
