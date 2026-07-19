package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestColumnTypeNameValidatesTemporalDeclarations(t *testing.T) {
	for _, good := range []string{"DATE", "DATETIME(6)", "TIMESTAMP", "TIME(3)", "YEAR"} {
		if got, err := columnTypeName([]string{good}); err != nil || got != good {
			t.Fatalf("columnTypeName(%s) = %q err %v", good, got, err)
		}
	}
	if _, err := columnTypeName([]string{"DATETIME(7)"}); err == nil {
		t.Fatalf("columnTypeName accepted an out-of-ceiling DATETIME precision")
	}
	if _, err := columnTypeName([]string{"YEAR(4)"}); err == nil {
		t.Fatalf("columnTypeName accepted an unsupported YEAR width")
	}
}

func TestTemporalTableMetadataIncludesFractionalPrecision(t *testing.T) {
	table := catalog.Table{
		Name:        "events",
		Columns:     []string{"at", "clock", "day", "year"},
		ColumnTypes: []string{"DATETIME(3)", "TIME(6)", "DATE", "YEAR"},
	}
	metadata := tableMetadata("app", "events", table, []int{0, 1, 2, 3})
	want := []byte{3, 6, 0, 0}
	for index, definition := range metadata {
		if definition.decimals != want[index] {
			t.Errorf("temporal column %q decimals = %d, want %d", definition.name, definition.decimals, want[index])
		}
	}
}

func temporalColumnTable() catalog.Table {
	return catalog.Table{
		Name:        "events",
		Columns:     []string{"at", "note"},
		ColumnTypes: []string{"DATETIME", ""},
	}
}

func TestCanonicalColumnValueTemporal(t *testing.T) {
	table := temporalColumnTable()
	if got, err := canonicalColumnValue(table, 0, "'2021-01-02 03:04:05'", 1); err != nil || got != "2021-01-02 03:04:05" {
		t.Fatalf("temporal column canonicalization = %q err %v", got, err)
	}
	if _, err := canonicalColumnValue(table, 0, "'2021-02-30 00:00:00'", 1); err == nil {
		t.Fatalf("invalid calendar datetime accepted")
	}
	if _, err := canonicalColumnValue(table, 0, "'0000-00-00 00:00:00'", 1); err == nil {
		t.Fatalf("zero datetime accepted")
	}
}

func TestMatcherValueTemporal(t *testing.T) {
	table := temporalColumnTable()
	// A predicate literal is canonicalized to the stored form so 'YYYY-MM-DD HH:MM:SS'
	// matches the written value regardless of surrounding whitespace.
	if got := matcherValue(table, 0, "' 2021-01-02 03:04:05 '"); got != "2021-01-02 03:04:05" {
		t.Errorf("temporal predicate literal = %q, want canonical datetime", got)
	}
	// A malformed literal keeps its scalar and simply matches no row.
	if got := matcherValue(table, 0, "'not-a-date'"); got != "not-a-date" {
		t.Errorf("malformed temporal predicate literal = %q, want raw fallback", got)
	}
}

func TestTimestampSessionOffsetStoresUTCAndRendersLocal(t *testing.T) {
	table := catalog.Table{
		Name:        "events",
		Columns:     []string{"at"},
		ColumnTypes: []string{"TIMESTAMP"},
	}
	stored, err := canonicalColumnValueAtOffset(table, 0, "'2021-01-02 03:04:05'", 1, 330)
	if err != nil || stored != "2021-01-01 21:34:05" {
		t.Fatalf("TIMESTAMP storage = %q err %v, want UTC value", stored, err)
	}
	if got := matcherValueAtOffset(table, 0, "'2021-01-02 03:04:05'", 330); got != stored {
		t.Errorf("TIMESTAMP matcher = %q, want %q", got, stored)
	}
	if got, err := renderStoredTemporalValue(table.ColumnTypes[0], stored, 330); err != nil || got != "2021-01-02 03:04:05" {
		t.Errorf("TIMESTAMP read = %q err %v, want session-local value", got, err)
	}
}

func TestPreparedDateToDatetimeConversionIsTypedOnly(t *testing.T) {
	table := catalog.Table{
		Name:        "events",
		Columns:     []string{"at"},
		ColumnTypes: []string{"DATETIME"},
	}
	prepared := preparedTemporalLiteral(mysqlTypeDate, "2021-01-02")
	if got, err := canonicalColumnValueAtOffset(table, 0, prepared, 1, 0); err != nil || got != "2021-01-02 00:00:00" {
		t.Errorf("typed DATE to DATETIME = %q err %v, want midnight conversion", got, err)
	}
	if _, err := canonicalColumnValueAtOffset(table, 0, "'2021-01-02'", 1, 0); err == nil {
		t.Fatal("text DATE to DATETIME was accepted")
	}
}
