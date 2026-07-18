package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestColumnTypeNameFoldsUnsignedAndRejectsCeilings(t *testing.T) {
	got, err := columnTypeName([]string{"INT", "UNSIGNED"})
	if err != nil || got != "INT UNSIGNED" {
		t.Fatalf("columnTypeName(INT UNSIGNED) = %q err %v", got, err)
	}
	if got, err := columnTypeName([]string{"BIGINT"}); err != nil || got != "BIGINT" {
		t.Fatalf("columnTypeName(BIGINT) = %q err %v", got, err)
	}
	if _, err := columnTypeName([]string{"DECIMAL(66,2)"}); err == nil {
		t.Fatalf("columnTypeName accepted an out-of-ceiling DECIMAL declaration")
	}
	if _, err := columnTypeName([]string{"BIT(65)"}); err == nil {
		t.Fatalf("columnTypeName accepted an out-of-ceiling BIT declaration")
	}
}

func TestCatalogColumnWireType(t *testing.T) {
	cases := []struct {
		typeName string
		wire     byte
		charset  uint16
	}{
		{"INT", mysqlTypeLong, mysqlCharsetBinary},
		{"DECIMAL(10,2)", mysqlTypeNewDecimal, mysqlCharsetBinary},
		{"BIT(8)", mysqlTypeBit, mysqlCharsetBinary},
		{"BOOLEAN", mysqlTypeTiny, mysqlCharsetBinary},
		{"VARCHAR(32)", mysqlTypeVarString, mysqlCharsetUTF8MB40900AICI},
		{"DATE", mysqlTypeVarString, mysqlCharsetUTF8MB40900AICI},
	}
	for _, c := range cases {
		wire, _, charset := catalogColumnWireType(c.typeName)
		if wire != c.wire || charset != c.charset {
			t.Errorf("catalogColumnWireType(%s) = (%#x,%d), want (%#x,%d)", c.typeName, wire, charset, c.wire, c.charset)
		}
	}
}

func numericColumnTable() catalog.Table {
	return catalog.Table{
		Name:        "t",
		Columns:     []string{"n", "note"},
		ColumnTypes: []string{"INT", ""},
	}
}

func TestCanonicalColumnValue(t *testing.T) {
	table := numericColumnTable()
	if got, err := canonicalColumnValue(table, 0, "007", 1); err != nil || got != "7" {
		t.Fatalf("numeric column canonicalization = %q err %v", got, err)
	}
	if got, err := canonicalColumnValue(table, 1, "'text'", 1); err != nil || got != "text" {
		t.Fatalf("typeless column passthrough = %q err %v", got, err)
	}
	if _, err := canonicalColumnValue(table, 0, "abc", 1); err == nil {
		t.Fatalf("malformed numeric column value accepted")
	}
}

func TestMatcherValue(t *testing.T) {
	table := numericColumnTable()
	if got := matcherValue(table, 0, "007"); got != "7" {
		t.Errorf("numeric predicate literal = %q, want 7", got)
	}
	if got := matcherValue(table, 1, "'text'"); got != "text" {
		t.Errorf("typeless predicate literal = %q, want text", got)
	}
	if got := matcherValue(table, 0, "abc"); got != "abc" {
		t.Errorf("malformed numeric predicate literal = %q, want raw fallback abc", got)
	}
}
