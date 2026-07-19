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
