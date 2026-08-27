// This file implements the v0.1 strict character and binary value contract:
// the utf8mb4 character families, the two supported collations
// (utf8mb4_0900_ai_ci and utf8mb4_bin), and the binary families. Invalid UTF-8,
// an assignment that exceeds a declared length, and a value past the fixed
// scalar ceiling all fail with a MySQL 8.4.11 error identity before any durable
// effect, so a rejected write never changes a table. v0.1 never silently
// truncates, pads away significant bytes, or reinterprets binary content as
// text. CHAR drops trailing U+0020 on store; BINARY(n) pads short values with 0x00.
package mysql

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	mysqlCharsetUTF8MB40900AICI uint16 = 255
	mysqlCharsetUTF8MB4Bin      uint16 = 46
)

// characterScalarCeiling is the fixed public ceiling on one character or binary
// scalar value, measured in bytes.
const characterScalarCeiling = 16 * 1024 * 1024

type characterKind int

const (
	characterNone characterKind = iota
	characterText
	characterBinary
)

type collationKind int

const (
	collationNone collationKind = iota
	collation0900AICI
	collationBin
)

func collationSQLName(kind collationKind) string {
	if kind == collationBin {
		return "utf8mb4_bin"
	}
	return "utf8mb4_0900_ai_ci"
}

func illegalMixOfCollations(left, right characterType) error {
	return sqlFailure{1267, "HY000", fmt.Sprintf("Illegal mix of collations (%s) and (%s) for operation '='", collationSQLName(left.collation), collationSQLName(right.collation))}
}

// characterType is the parsed description of a declared character or binary
// column. A characterNone kind means the declaration belongs to another
// contract (numeric, temporal, or an unknown legacy type).
type characterType struct {
	kind      characterKind
	bounded   bool
	length    int
	collation collationKind
	wire      byte
}

type characterFamily struct {
	kind    characterKind
	bounded bool
	wire    byte
}

var characterFamilies = map[string]characterFamily{
	"CHAR":       {kind: characterText, bounded: true, wire: mysqlTypeString},
	"VARCHAR":    {kind: characterText, bounded: true, wire: mysqlTypeVarString},
	"TINYTEXT":   {kind: characterText, bounded: false, wire: mysqlTypeBlob},
	"TEXT":       {kind: characterText, bounded: false, wire: mysqlTypeBlob},
	"MEDIUMTEXT": {kind: characterText, bounded: false, wire: mysqlTypeBlob},
	"LONGTEXT":   {kind: characterText, bounded: false, wire: mysqlTypeBlob},
	"BINARY":     {kind: characterBinary, bounded: true, wire: mysqlTypeString},
	"VARBINARY":  {kind: characterBinary, bounded: true, wire: mysqlTypeVarString},
	"TINYBLOB":   {kind: characterBinary, bounded: false, wire: mysqlTypeBlob},
	"BLOB":       {kind: characterBinary, bounded: false, wire: mysqlTypeBlob},
	"MEDIUMBLOB": {kind: characterBinary, bounded: false, wire: mysqlTypeBlob},
	"LONGBLOB":   {kind: characterBinary, bounded: false, wire: mysqlTypeBlob},
}

// parseCharacterType parses a recorded column type, including any trailing
// COLLATE clause folded in at declaration time. A characterNone kind means the
// declaration is not a character or binary family; a non-nil error means the
// declaration violates a public ceiling or names an unsupported collation.
func parseCharacterType(typeName string) (characterType, error) {
	base, argument, collationName := splitCharacterType(typeName)
	family, ok := characterFamilies[base]
	if !ok {
		return characterType{}, nil
	}
	length, err := characterLength(family, argument)
	if err != nil {
		return characterType{}, err
	}
	collation, err := resolveCollation(family.kind, collationName)
	if err != nil {
		return characterType{}, err
	}
	return characterType{kind: family.kind, bounded: family.bounded, length: length, collation: collation, wire: family.wire}, nil
}

// splitCharacterType returns the upper-cased base name, the parenthesised length
// argument, and the collation name from any trailing COLLATE clause.
func splitCharacterType(typeName string) (string, string, string) {
	normalized := strings.ToUpper(strings.TrimSpace(typeName))
	collationName := ""
	if index := strings.Index(normalized, " COLLATE "); index >= 0 {
		collationName = strings.TrimSpace(normalized[index+len(" COLLATE "):])
		normalized = strings.TrimSpace(normalized[:index])
	}
	open := strings.IndexByte(normalized, '(')
	if open < 0 {
		return normalized, "", collationName
	}
	end := strings.LastIndexByte(normalized, ')')
	if end <= open {
		return normalized, "", collationName
	}
	return strings.TrimSpace(normalized[:open]), normalized[open+1 : end], collationName
}

func characterLength(family characterFamily, argument string) (int, error) {
	if !family.bounded {
		if strings.TrimSpace(argument) != "" {
			return 0, sqlFailure{1064, "42000", "unexpected length for unbounded character type"}
		}
		return 0, nil
	}
	if strings.TrimSpace(argument) == "" {
		return 1, nil
	}
	length, err := strconv.Atoi(strings.TrimSpace(argument))
	if err != nil || length < 0 {
		return 0, sqlFailure{1064, "42000", "invalid character length"}
	}
	// The ceiling bounds a single scalar value in bytes; comparing the declared
	// length (characters for text, bytes for binary) against it is a deliberately
	// generous upper bound, since every stored value is separately held to the
	// byte ceiling in canonicalCharacterValue.
	if length > characterScalarCeiling {
		return 0, sqlFailure{1074, "42000", fmt.Sprintf("Column length too big (max = %d)", characterScalarCeiling)}
	}
	return length, nil
}

func resolveCollation(kind characterKind, collationName string) (collationKind, error) {
	if collationName == "" {
		if kind == characterText {
			return collation0900AICI, nil
		}
		return collationNone, nil
	}
	if kind != characterText {
		return collationNone, sqlFailure{1253, "42000", "COLLATE is not applicable to a binary type"}
	}
	switch strings.ToUpper(collationName) {
	case "UTF8MB4_0900_AI_CI":
		return collation0900AICI, nil
	case "UTF8MB4_BIN":
		return collationBin, nil
	default:
		return collationNone, sqlFailure{1273, "HY000", fmt.Sprintf("Unsupported collation '%s'", collationName)}
	}
}

// characterModifierTypeName folds a trailing CHARACTER SET / COLLATE clause into
// a canonical recorded type string, rejecting an unsupported character set or a
// clause applied to a non-character type. The modifiers slice holds the declared
// tokens that follow the base type (and any UNSIGNED modifier).
func characterModifierTypeName(base string, modifiers []string) (string, error) {
	charset, collation, err := scanCharacterModifiers(modifiers)
	if err != nil {
		return "", err
	}
	if charset == "" && collation == "" {
		return base, nil
	}
	if err := validateCharacterModifierTarget(base, charset); err != nil {
		return "", err
	}
	if collation == "" {
		return base, nil
	}
	if _, err := resolveCollation(characterText, collation); err != nil {
		return "", err
	}
	return base + " COLLATE " + strings.ToLower(collation), nil
}

// validateCharacterModifierTarget rejects a CHARACTER SET or COLLATE clause on a
// non-character base type and any character set other than the supported
// utf8mb4.
func validateCharacterModifierTarget(base, charset string) error {
	family, ok := characterFamilies[strings.ToUpper(splitBaseName(base))]
	if !ok || family.kind != characterText {
		return sqlFailure{1253, "42000", "character set or collation is not applicable to this type"}
	}
	if charset != "" && strings.ToUpper(charset) != "UTF8MB4" {
		return sqlFailure{1115, "42000", fmt.Sprintf("Unknown character set: '%s'", charset)}
	}
	return nil
}

func splitBaseName(base string) string {
	name, _, _ := strings.Cut(base, "(")
	return name
}

// scanCharacterModifiers reads the optional CHARACTER SET and COLLATE clauses
// from the declared modifier tokens. Any other token is an unsupported column
// modifier and fails.
func scanCharacterModifiers(modifiers []string) (string, string, error) {
	charset, collation := "", ""
	count := len(modifiers)
	for index := 0; index < count; index++ {
		token := strings.ToUpper(modifiers[index])
		switch token {
		case "CHARACTER":
			if index+2 >= len(modifiers) || strings.ToUpper(modifiers[index+1]) != "SET" {
				return "", "", sqlFailure{1064, "42000", "malformed CHARACTER SET clause"}
			}
			charset, index = modifiers[index+2], index+2
		case "CHARSET":
			if index+1 >= len(modifiers) {
				return "", "", sqlFailure{1064, "42000", "malformed CHARSET clause"}
			}
			charset, index = modifiers[index+1], index+1
		case "COLLATE":
			if index+1 >= len(modifiers) {
				return "", "", sqlFailure{1064, "42000", "malformed COLLATE clause"}
			}
			collation, index = modifiers[index+1], index+1
		default:
			return "", "", sqlFailure{1235, "42000", "unsupported column modifier"}
		}
	}
	return charset, collation, nil
}

// canonicalCharacterValue validates a supplied literal against a character or
// binary column and returns the stored representation. Invalid UTF-8,
// an over-length assignment, and a value past the scalar ceiling all fail with a
// MySQL error identity so the caller can reject the write before durability.
func canonicalCharacterValue(typ characterType, value, column string, row int) (string, error) {
	if len(value) > characterScalarCeiling {
		return "", dataTooLong(column, row)
	}
	if typ.kind == characterText {
		return canonicalTextValue(typ, value, column, row)
	}
	return canonicalBinaryValue(typ, value, column, row)
}

func canonicalTextValue(typ characterType, value, column string, row int) (string, error) {
	if !utf8.ValidString(value) {
		return "", incorrectStringValue(column, value, row)
	}
	if typ.bounded && utf8.RuneCountInString(value) > typ.length {
		return "", dataTooLong(column, row)
	}
	if typ.wire == mysqlTypeString {
		return strings.TrimRight(value, " "), nil
	}
	return value, nil
}

func canonicalBinaryValue(typ characterType, value, column string, row int) (string, error) {
	if typ.bounded && len(value) > typ.length {
		return "", dataTooLong(column, row)
	}
	if typ.bounded && typ.wire == mysqlTypeString && len(value) < typ.length {
		return value + strings.Repeat("\x00", typ.length-len(value)), nil
	}
	return value, nil
}

// characterComparisonKey returns the value transformed so that byte equality of
// two keys matches the column collation. utf8mb4_bin compares bytewise;
// utf8mb4_0900_ai_ci is accent- and case-insensitive, so its key strips
// combining marks and applies full case folding over the canonical
// decomposition. A non-character column keeps its value verbatim. This is a
// decomposition-based approximation of the DUCET collation; the collation
// machinery is an implementation choice, and observable equality is pinned by
// differential conformance rather than by this representation.
func characterComparisonKey(typ characterType, value string) string {
	if typ.kind != characterText || typ.collation != collation0900AICI {
		return value
	}
	decomposed := norm.NFD.String(value)
	stripped := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, decomposed)
	return cases.Fold().String(stripped)
}

// characterWireCharset reports the result-column character set that matches the
// column collation: the two utf8mb4 collations for text, and the binary
// character set for every binary family.
func characterWireCharset(typ characterType) uint16 {
	if typ.kind == characterBinary {
		return mysqlCharsetBinary
	}
	if typ.collation == collationBin {
		return mysqlCharsetUTF8MB4Bin
	}
	return mysqlCharsetUTF8MB40900AICI
}

func dataTooLong(column string, row int) error {
	return sqlFailure{1406, "22001", fmt.Sprintf("Data too long for column '%s' at row %d", column, row)}
}

func incorrectStringValue(column, value string, row int) error {
	return sqlFailure{1366, "HY000", fmt.Sprintf("Incorrect string value: '%s' for column '%s' at row %d", value, column, row)}
}
