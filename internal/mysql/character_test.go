package mysql

import (
	"strings"
	"testing"
)

func TestParseCharacterTypeFamilies(t *testing.T) {
	cases := []struct {
		typeName  string
		kind      characterKind
		bounded   bool
		length    int
		collation collationKind
		wire      byte
	}{
		{"VARCHAR(32)", characterText, true, 32, collation0900AICI, mysqlTypeVarString},
		{"CHAR", characterText, true, 1, collation0900AICI, mysqlTypeString},
		{"CHAR(10)", characterText, true, 10, collation0900AICI, mysqlTypeString},
		{"TEXT", characterText, false, 0, collation0900AICI, mysqlTypeBlob},
		{"LONGTEXT", characterText, false, 0, collation0900AICI, mysqlTypeBlob},
		{"VARCHAR(8) COLLATE utf8mb4_bin", characterText, true, 8, collationBin, mysqlTypeVarString},
		{"BINARY(4)", characterBinary, true, 4, collationNone, mysqlTypeString},
		{"VARBINARY(20)", characterBinary, true, 20, collationNone, mysqlTypeVarString},
		{"BLOB", characterBinary, false, 0, collationNone, mysqlTypeBlob},
		{"INT", characterNone, false, 0, collationNone, 0},
		{"UNKNOWNTYPE", characterNone, false, 0, collationNone, 0},
	}
	for _, c := range cases {
		t.Run(c.typeName, func(t *testing.T) {
			typ, err := parseCharacterType(c.typeName)
			if err != nil {
				t.Fatalf("parseCharacterType(%q) error: %v", c.typeName, err)
			}
			if typ.kind != c.kind || typ.bounded != c.bounded || typ.length != c.length || typ.collation != c.collation || typ.wire != c.wire {
				t.Fatalf("parseCharacterType(%q) = %+v", c.typeName, typ)
			}
		})
	}
}

func TestParseCharacterTypeRejections(t *testing.T) {
	cases := []struct {
		typeName string
		code     uint16
	}{
		{"VARCHAR(abc)", 1064},
		{"VARCHAR(999999999999)", 1074},
		{"TEXT(10)", 1064},
		{"VARCHAR(8) COLLATE utf8mb4_general_ci", 1273},
	}
	for _, c := range cases {
		t.Run(c.typeName, func(t *testing.T) {
			_, err := parseCharacterType(c.typeName)
			assertFailureCode(t, err, c.code)
		})
	}
}

func TestCanonicalCharacterValue(t *testing.T) {
	text8, _ := parseCharacterType("VARCHAR(8)")
	char4, _ := parseCharacterType("CHAR(4)")
	binary4, _ := parseCharacterType("BINARY(4)")
	unbounded, _ := parseCharacterType("TEXT")

	if got, err := canonicalCharacterValue(text8, "café", "c", 1); err != nil || got != "café" {
		t.Fatalf("valid utf8 = %q, %v", got, err)
	}
	if got, err := canonicalCharacterValue(unbounded, strings.Repeat("x", 100), "c", 1); err != nil || len(got) != 100 {
		t.Fatalf("unbounded length preserved = %q, %v", got, err)
	}
	if _, err := canonicalCharacterValue(text8, "\xff\xfe", "c", 1); !isFailureCode(err, 1366) {
		t.Fatalf("invalid utf8 not rejected: %v", err)
	}
	if _, err := canonicalCharacterValue(text8, "123456789", "c", 2); !isFailureCode(err, 1406) {
		t.Fatalf("over-length text not rejected: %v", err)
	}
	// Multi-byte characters count as scalars, not bytes: eight accented letters fit.
	if _, err := canonicalCharacterValue(text8, "áéíóúñüç", "c", 1); err != nil {
		t.Fatalf("eight-scalar value rejected: %v", err)
	}
	if _, err := canonicalCharacterValue(binary4, "12345", "c", 1); !isFailureCode(err, 1406) {
		t.Fatalf("over-length binary not rejected: %v", err)
	}
	if got, err := canonicalCharacterValue(char4, "ab  ", "c", 1); err != nil || got != "ab" {
		t.Fatalf("CHAR trailing spaces kept = %q, %v", got, err)
	}
	if got, err := canonicalCharacterValue(binary4, "ab", "c", 1); err != nil || got != "ab\x00\x00" {
		t.Fatalf("BINARY not padded = %q, %v", got, err)
	}
	if got, err := canonicalCharacterValue(binary4, "\xff\x00\x01", "c", 1); err != nil || got != "\xff\x00\x01\x00" {
		t.Fatalf("BINARY value = %q, %v", got, err)
	}
}

func TestCanonicalCharacterValueScalarCeiling(t *testing.T) {
	unbounded, _ := parseCharacterType("LONGTEXT")
	oversized := strings.Repeat("a", characterScalarCeiling+1)
	if _, err := canonicalCharacterValue(unbounded, oversized, "c", 1); !isFailureCode(err, 1406) {
		t.Fatalf("scalar ceiling not enforced: %v", err)
	}
}

func TestCharacterComparisonKey(t *testing.T) {
	ai, _ := parseCharacterType("VARCHAR(16)")
	bin, _ := parseCharacterType("VARCHAR(16) COLLATE utf8mb4_bin")
	numeric := characterType{kind: characterNone}

	if characterComparisonKey(ai, "Café") != characterComparisonKey(ai, "cafe") {
		t.Fatalf("ai_ci should match case- and accent-insensitively")
	}
	if characterComparisonKey(bin, "Café") == characterComparisonKey(bin, "cafe") {
		t.Fatalf("utf8mb4_bin must be bytewise")
	}
	if characterComparisonKey(bin, "abc") != "abc" {
		t.Fatalf("bin key should be verbatim")
	}
	if characterComparisonKey(numeric, "AbC") != "AbC" {
		t.Fatalf("non-character key should be verbatim")
	}
}

func TestCharacterWireCharset(t *testing.T) {
	ai, _ := parseCharacterType("VARCHAR(16)")
	bin, _ := parseCharacterType("VARCHAR(16) COLLATE utf8mb4_bin")
	blob, _ := parseCharacterType("BLOB")
	if characterWireCharset(ai) != mysqlCharsetUTF8MB40900AICI {
		t.Fatalf("default text charset")
	}
	if characterWireCharset(bin) != mysqlCharsetUTF8MB4Bin {
		t.Fatalf("bin charset")
	}
	if characterWireCharset(blob) != mysqlCharsetBinary {
		t.Fatalf("binary charset")
	}
}

func TestCharacterModifierTypeName(t *testing.T) {
	cases := []struct {
		base      string
		modifiers []string
		want      string
	}{
		{"VARCHAR(8)", nil, "VARCHAR(8)"},
		{"VARCHAR(8)", []string{"COLLATE", "utf8mb4_bin"}, "VARCHAR(8) COLLATE utf8mb4_bin"},
		{"VARCHAR(8)", []string{"CHARACTER", "SET", "utf8mb4"}, "VARCHAR(8)"},
		{"VARCHAR(8)", []string{"CHARSET", "utf8mb4", "COLLATE", "utf8mb4_0900_ai_ci"}, "VARCHAR(8) COLLATE utf8mb4_0900_ai_ci"},
	}
	for _, c := range cases {
		got, err := characterModifierTypeName(c.base, c.modifiers)
		if err != nil || got != c.want {
			t.Fatalf("characterModifierTypeName(%q,%v) = %q,%v want %q", c.base, c.modifiers, got, err, c.want)
		}
	}
}

func TestCharacterModifierTypeNameRejections(t *testing.T) {
	cases := []struct {
		base      string
		modifiers []string
		code      uint16
	}{
		{"INT", []string{"COLLATE", "utf8mb4_bin"}, 1253},
		{"VARCHAR(8)", []string{"CHARACTER", "SET", "latin1"}, 1115},
		{"VARCHAR(8)", []string{"COLLATE", "nope"}, 1273},
		{"VARCHAR(8)", []string{"COLLATE"}, 1064},
		{"VARCHAR(8)", []string{"CHARACTER", "utf8mb4"}, 1064},
		{"VARCHAR(8)", []string{"NONSENSE"}, 1235},
	}
	for _, c := range cases {
		_, err := characterModifierTypeName(c.base, c.modifiers)
		assertFailureCode(t, err, c.code)
	}
}

func isFailureCode(err error, code uint16) bool {
	failure, ok := err.(sqlFailure)
	return ok && failure.code == code
}

func assertFailureCode(t *testing.T, err error, code uint16) {
	t.Helper()
	if !isFailureCode(err, code) {
		t.Fatalf("error = %v, want code %d", err, code)
	}
}
