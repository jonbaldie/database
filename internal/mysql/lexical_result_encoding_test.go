package mysql

import (
	"bytes"
	"encoding/binary"
	"math"
	"net"
	"reflect"
	"testing"
)

func TestSplitQualifiedIdentifierPreservesQuotedNames(t *testing.T) {
	tests := []struct {
		name  string
		input string
		parts []string
		valid bool
	}{
		{name: "quoted dots and escaped backticks", input: " `data.base` . `row``name` ", parts: []string{"data.base", "row`name"}, valid: true},
		{name: "quoted identifier trailing whitespace", input: "`table` ", parts: []string{"table"}, valid: true},
		{name: "single bare identifier", input: "table", parts: []string{"table"}, valid: true},
		{name: "trailing SQL", input: "`table` SELECT 1", valid: false},
		{name: "unterminated quote", input: "`table", valid: false},
		{name: "legacy trailing separator", input: "namespace.", parts: []string{"namespace"}, valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts, valid := splitQualifiedIdentifier(test.input)
			if valid != test.valid || !reflect.DeepEqual(parts, test.parts) {
				t.Fatalf("splitQualifiedIdentifier(%q) = (%q, %t), want (%q, %t)", test.input, parts, valid, test.parts, test.valid)
			}
		})
	}
}

func TestConsumeIdentifierKeepsDefinitionRemainder(t *testing.T) {
	name, remainder, valid := consumeIdentifier(" `row``name` VARCHAR(255) NOT NULL")
	if !valid || name != "row`name" || remainder != " VARCHAR(255) NOT NULL" {
		t.Fatalf("consumeIdentifier returned (%q, %q, %t)", name, remainder, valid)
	}
	if _, _, valid := consumeIdentifier("namespace.table INT"); valid {
		t.Fatal("qualified column token was accepted")
	}
}

func TestPreparedPlaceholdersIgnoreQuotedQuestionMarks(t *testing.T) {
	query := "SELECT '?', \"?\", `?`, ?, 'it''s ?'"
	positions := preparedPlaceholders(query)
	if len(positions) != 1 || query[positions[0]] != '?' {
		t.Fatalf("preparedPlaceholders(%q) = %v, want one bind placeholder", query, positions)
	}
}

func TestPreparedPlaceholderLimitCountsOnlyBindMarkers(t *testing.T) {
	count, withinLimit := countPreparedParameters("SELECT '?', ?, ?", 1)
	if count != 2 || withinLimit {
		t.Fatalf("countPreparedParameters returned (%d, %t), want (2, false)", count, withinLimit)
	}
	count, withinLimit = countPreparedParameters("SELECT '?'", 0)
	if count != 0 || !withinLimit {
		t.Fatalf("quoted marker count returned (%d, %t), want (0, true)", count, withinLimit)
	}
}

func TestLengthEncodedValuesPreserveBoundariesAndNull(t *testing.T) {
	for _, value := range []string{"", string(make([]byte, 250)), string(make([]byte, 251)), string(make([]byte, 65536))} {
		payload := lengthEncodedString(value)
		decoded, next, valid := readLengthEncoded(payload, 0)
		if !valid || string(decoded) != value || next != len(payload) {
			t.Fatalf("length encoding round trip failed for %d bytes: (%d, %t)", len(value), next, valid)
		}
	}
	decoded, next, valid := readLengthEncoded([]byte{0xfb}, 0)
	if !valid || decoded != nil || next != 1 {
		t.Fatalf("NULL length encoding = (%v, %d, %t)", decoded, next, valid)
	}
	if _, next, valid := readLengthEncoded([]byte{0xfc}, 0); valid || next != 1 {
		t.Fatalf("truncated length encoding = (%d, %t)", next, valid)
	}
}

func TestBinaryRowPreservesValuesAndNullBitmap(t *testing.T) {
	metadata := []columnMetadata{
		{typ: mysqlTypeLongLong},
		{typ: mysqlTypeLongLong, flags: mysqlUnsignedFlag},
		{typ: mysqlTypeLong},
		{typ: mysqlTypeDouble},
		{typ: mysqlTypeVarString},
	}
	row, err := binaryRow([]string{"-2", "42", "3", "1.5", "ignored"}, 0, [][]bool{{false, false, false, false, true}}, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if row[0] != 0 || row[1] != 0x40 {
		t.Fatalf("binary NULL bitmap = %x, want 0040", row[:2])
	}
	if value := int64(binary.LittleEndian.Uint64(row[2:10])); value != -2 {
		t.Fatalf("signed integer = %d, want -2", value)
	}
	if value := binary.LittleEndian.Uint64(row[10:18]); value != 42 {
		t.Fatalf("unsigned integer = %d, want 42", value)
	}
	if value := int32(binary.LittleEndian.Uint32(row[18:22])); value != 3 {
		t.Fatalf("integer = %d, want 3", value)
	}
	if value := math.Float64frombits(binary.LittleEndian.Uint64(row[22:30])); value != 1.5 {
		t.Fatalf("double = %v, want 1.5", value)
	}
}

func TestBinaryRowEncodesTemporalValuesInBinaryProtocol(t *testing.T) {
	cases := []struct {
		name       string
		value      string
		definition columnMetadata
		want       []byte
	}{
		{name: "date", value: "2021-01-02", definition: columnMetadata{typ: mysqlTypeDate}, want: []byte{4, 0xE5, 0x07, 1, 2}},
		{name: "datetime", value: "2021-01-02 03:04:05.123456", definition: columnMetadata{typ: mysqlTypeDatetime, decimals: 6}, want: []byte{11, 0xE5, 0x07, 1, 2, 3, 4, 5, 0x40, 0xE2, 0x01, 0x00}},
		{name: "zero clock compresses", value: "2021-01-02 00:00:00", definition: columnMetadata{typ: mysqlTypeDatetime}, want: []byte{4, 0xE5, 0x07, 1, 2}},
		{name: "time", value: "-38:00:00.123456", definition: columnMetadata{typ: mysqlTypeTime, decimals: 6}, want: []byte{12, 1, 1, 0, 0, 0, 14, 0, 0, 0x40, 0xE2, 0x01, 0x00}},
		{name: "year", value: "2021", definition: columnMetadata{typ: mysqlTypeYear}, want: []byte{0xE5, 0x07}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := encodeBinaryValue(test.value, test.definition)
			if err != nil || !bytes.Equal(got, test.want) {
				t.Fatalf("encodeBinaryValue(%q) = %x err %v, want %x", test.value, got, err, test.want)
			}
		})
	}
}

func TestTextResultPreservesMetadataRowsAndPacketSequence(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		result := &queryResult{
			columns:  []string{"value"},
			rows:     [][]string{{""}},
			nulls:    [][]bool{{true}},
			metadata: []columnMetadata{{catalog: "def", name: "value", typ: mysqlTypeVarString}},
		}
		done <- writeResult(server, 7, result, 1024)
	}()
	for sequence := byte(7); sequence != 12; sequence++ {
		actual, payload, err := readPacket(client, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if actual != sequence {
			t.Fatalf("packet sequence = %d, want %d", actual, sequence)
		}
		if sequence == 10 && !bytes.Equal(payload, []byte{0xfb}) {
			t.Fatalf("text NULL row payload = %x, want fb", payload)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestResultStreamWritesRecoverableSQLErrorTerminator(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		result := &queryResult{
			columns:  []string{"value"},
			metadata: []columnMetadata{{catalog: "def", name: "value", typ: mysqlTypeLongLong}},
			stream: func(yield func([]string, []bool) error) error {
				if err := yield([]string{"1"}, []bool{false}); err != nil {
					return err
				}
				return sqlFailure{1365, "22012", "division by zero"}
			},
		}
		done <- writeResult(server, 7, result, 1024)
	}()
	var final []byte
	for sequence := byte(7); sequence != 12; sequence++ {
		actual, payload, err := readPacket(client, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if actual != sequence {
			t.Fatalf("packet sequence = %d, want %d", actual, sequence)
		}
		final = payload
	}
	if len(final) < 3 || final[0] != 0xff || binary.LittleEndian.Uint16(final[1:3]) != 1365 {
		t.Fatalf("final packet = %x", final)
	}
	if err := <-done; err != nil {
		t.Fatalf("stream SQL error closed conversation: %v", err)
	}
}
