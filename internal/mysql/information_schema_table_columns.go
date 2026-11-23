package mysql

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jonbaldie/database/internal/catalog"
)

func informationSchemaTableRow(namespace string, table catalog.Table, virtual bool) []metadataValue {
	tableType := "BASE TABLE"
	rows, autoIncrement := metadataValue{value: strconv.Itoa(len(table.Rows))}, informationSchemaAutoIncrement(table)
	if virtual {
		tableType = "SYSTEM VIEW"
		rows, autoIncrement = metadataValue{null: true}, metadataValue{null: true}
	}
	// RowCount is also the server-managed statistic supplied to the planner.
	// No physical allocation, format, version, or maintenance fact is public.
	return []metadataValue{
		{value: informationSchemaCatalogName}, {value: namespace}, {value: table.Name}, {value: tableType},
		{value: "DATABASE"}, {null: true}, {null: true}, rows,
		{null: true}, {null: true}, {null: true}, {null: true}, {null: true}, autoIncrement,
		{null: true}, {null: true}, {null: true}, {value: "utf8mb4_0900_ai_ci"}, {null: true},
		{value: ""}, {value: ""},
	}
}

func informationSchemaColumnRow(namespace string, table catalog.Table, index int, privileges string) []metadataValue {
	attribute := catalog.ColumnAttributeAt(table, index)
	dataType, columnType := informationSchemaType(table, index)
	defaultValue := metadataValue{null: true}
	if attribute.HasDefault && attribute.Default != storedSQLNullValue {
		defaultValue = metadataValue{value: attribute.Default}
	}
	facts := informationSchemaTypeFacts(columnType.value)
	return []metadataValue{
		{value: informationSchemaCatalogName}, {value: namespace}, {value: table.Name}, {value: table.Columns[index]},
		{value: strconv.Itoa(index + 1)}, defaultValue, {value: nullLabel(attribute)}, dataType,
		facts.characterLength, facts.octetLength, facts.numericPrecision, facts.numericScale, facts.datetimePrecision,
		facts.characterSet, facts.collation, columnType, {value: showColumnKey(table, index)},
		informationSchemaColumnExtra(table, index), {value: privileges}, {value: ""}, {value: ""}, {null: true},
	}
}

// Use the same committed authorization snapshot as the catalog rows.
func informationSchemaColumnPrivileges(s *session, definition catalog.Definition, namespace string) string {
	privileges := []string{}
	account := catalog.Account{}
	unrestricted := s == nil || s.username == ""
	if s != nil && s.username != "" {
		var found bool
		account, found = definition.Accounts[s.username]
		unrestricted = !found && s.unconfiguredAccountPermitted()
	}
	for _, grant := range []struct{ name, spelling string }{
		{"DATA_READ", "select"}, {"DATA_WRITE", "insert,update"}, {"SCHEMA_MANAGEMENT", "references"},
	} {
		if unrestricted || (!account.Locked && accountGrantIndex(account, grant.name, namespace) >= 0) {
			privileges = append(privileges, grant.spelling)
		}
	}
	return strings.Join(privileges, ",")
}

const informationSchemaEnumFlag uint16 = 1 << 8

// Catalog enums are finite metadata fields, not a user-table type extension.
func informationSchemaEnumLength(typeName string) int {
	if !strings.HasPrefix(typeName, "ENUM(") {
		return 0
	}
	maximum := 0
	for _, value := range splitCSV(strings.TrimSuffix(typeName[5:], ")")) {
		length := utf8.RuneCountInString(strings.Trim(value, "'"))
		if length > maximum {
			maximum = length
		}
	}
	return maximum
}

type informationSchemaColumnTypeFacts struct {
	characterLength, octetLength, numericPrecision, numericScale metadataValue
	datetimePrecision, characterSet, collation                   metadataValue
}

func informationSchemaTypeFacts(typeName string) informationSchemaColumnTypeFacts {
	null := metadataValue{null: true}
	facts := informationSchemaColumnTypeFacts{null, null, null, null, null, null, null}
	character, _ := parseCharacterType(typeName)
	if length := informationSchemaEnumLength(typeName); length > 0 {
		character = characterType{kind: characterText, bounded: true, length: length, collation: collation0900AICI}
	}
	if character.kind != characterNone {
		informationSchemaCharacterFacts(&facts, typeName, character)
	}
	numeric, _ := parseNumericType(typeName)
	if numeric.kind != numericNone {
		facts.numericPrecision, facts.numericScale = informationSchemaNumericFacts(numeric)
	}
	temporal, _ := parseTemporalType(typeName)
	if temporal.kind == temporalDate || temporal.kind == temporalTime || temporal.kind == temporalDatetime || temporal.kind == temporalTimestamp {
		facts.datetimePrecision = metadataValue{value: strconv.Itoa(temporal.precision)}
	}
	if temporal.kind == temporalYear {
		facts.numericPrecision, facts.numericScale = metadataValue{value: "4"}, metadataValue{value: "0"}
	}
	return facts
}

func informationSchemaCharacterFacts(facts *informationSchemaColumnTypeFacts, typeName string, typ characterType) {
	length, octets := uint64(typ.length), uint64(typ.length)
	if typ.bounded && typ.kind == characterText {
		octets *= 4
	}
	if !typ.bounded {
		base, _, _ := splitCharacterType(typeName)
		length = informationSchemaLargeCharacterLengths[base]
		octets = length
	}
	facts.characterLength = metadataValue{value: strconv.FormatUint(length, 10)}
	facts.octetLength = metadataValue{value: strconv.FormatUint(octets, 10)}
	if typ.kind == characterText {
		facts.characterSet = metadataValue{value: "utf8mb4"}
		facts.collation = metadataValue{value: collationSQLName(typ.collation)}
	}
}

var informationSchemaLargeCharacterLengths = map[string]uint64{
	"TINYTEXT": 255, "TINYBLOB": 255,
	"TEXT": 65535, "BLOB": 65535,
	"MEDIUMTEXT": 16777215, "MEDIUMBLOB": 16777215,
	"LONGTEXT": 4294967295, "LONGBLOB": 4294967295,
}

func informationSchemaNumericFacts(typ numericType) (metadataValue, metadataValue) {
	precision, scale := 0, metadataValue{value: "0"}
	switch typ.kind {
	case numericInteger, numericBoolean:
		maximum := uint64(typ.smax)
		if typ.unsigned {
			maximum = typ.umax
		}
		precision = len(strconv.FormatUint(maximum, 10))
	case numericDecimal:
		precision, scale = typ.precision, metadataValue{value: strconv.Itoa(typ.scale)}
	case numericFloat:
		precision, scale = 12, metadataValue{null: true}
		if typ.sixtyFour {
			precision = 22
		}
	case numericBit:
		precision, scale = typ.width, metadataValue{null: true}
	}
	return metadataValue{value: strconv.Itoa(precision)}, scale
}
