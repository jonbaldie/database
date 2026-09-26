package mysql

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

func TestStreamWriterPacketFramingAndSequence(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 4, 1024)

	payload1 := []byte("hello")
	if err := writer.WritePacket(payload1); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	// First packet: 3 bytes length (5, 0, 0), 1 byte sequence (4), payload "hello"
	expectedHeader1 := []byte{5, 0, 0, 4}
	got := buf.Bytes()
	if !bytes.Equal(got[:4], expectedHeader1) {
		t.Fatalf("packet 1 header = %v, want %v", got[:4], expectedHeader1)
	}
	if !bytes.Equal(got[4:9], payload1) {
		t.Fatalf("packet 1 payload = %q, want %q", got[4:9], payload1)
	}
	if writer.Sequence() != 5 {
		t.Fatalf("writer sequence = %d, want 5", writer.Sequence())
	}
	if writer.PacketsWritten() != 1 {
		t.Fatalf("packets written = %d, want 1", writer.PacketsWritten())
	}

	payload2 := []byte("world")
	if err := writer.WritePacket(payload2); err != nil {
		t.Fatalf("WritePacket 2: %v", err)
	}
	// Second packet: 3 bytes length (5, 0, 0), 1 byte sequence (5), payload "world"
	expectedHeader2 := []byte{5, 0, 0, 5}
	if !bytes.Equal(got[9:13], expectedHeader2) {
		t.Fatalf("packet 2 header = %v, want %v", got[9:13], expectedHeader2)
	}
	if !bytes.Equal(got[13:18], payload2) {
		t.Fatalf("packet 2 payload = %q, want %q", got[13:18], payload2)
	}
	if writer.Sequence() != 6 {
		t.Fatalf("writer sequence = %d, want 6", writer.Sequence())
	}
	if writer.PacketsWritten() != 2 {
		t.Fatalf("packets written = %d, want 2", writer.PacketsWritten())
	}
}

func TestStreamWriterBoundedPacketEnforcesLimit(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 0, 100)

	smallPayload := make([]byte, 50)
	if err := writer.WritePacket(smallPayload); err != nil {
		t.Fatalf("WritePacket(50): %v", err)
	}

	largePayload := make([]byte, 101)
	err := writer.WritePacket(largePayload)
	if err == nil {
		t.Fatal("WritePacket(101) expected error for exceeding max packet, got nil")
	}
	// Buffer should only have written the small packet (4 byte header + 50 bytes = 54)
	if buf.Len() != 54 {
		t.Fatalf("buf.Len() = %d, want 54", buf.Len())
	}
}

func TestStreamWriterMultiPacketSplit(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 1, 0) // maxPacket 0 means unbounded

	// Create payload of maximumPacketFrame + 5 bytes
	payload := make([]byte, maximumPacketFrame+5)
	payload[0] = 0xAA
	payload[maximumPacketFrame+4] = 0xBB

	if err := writer.WritePacket(payload); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	if writer.PacketsWritten() != 2 {
		t.Fatalf("packets written = %d, want 2", writer.PacketsWritten())
	}
	if writer.Sequence() != 3 {
		t.Fatalf("writer sequence = %d, want 3", writer.Sequence())
	}

	data := buf.Bytes()
	// Frame 1 header: 3 bytes 0xff, 0xff, 0xff; 1 byte sequence 1
	if !bytes.Equal(data[:4], []byte{0xff, 0xff, 0xff, 1}) {
		t.Fatalf("frame 1 header = %v, want [255 255 255 1]", data[:4])
	}
	if data[4] != 0xAA {
		t.Fatalf("frame 1 first payload byte = %x, want AA", data[4])
	}

	// Frame 2 header starts after 4 + maximumPacketFrame
	frame2Offset := 4 + maximumPacketFrame
	if !bytes.Equal(data[frame2Offset:frame2Offset+4], []byte{5, 0, 0, 2}) {
		t.Fatalf("frame 2 header = %v, want [5 0 0 2]", data[frame2Offset:frame2Offset+4])
	}
	if data[frame2Offset+4+4] != 0xBB {
		t.Fatalf("frame 2 last payload byte = %x, want BB", data[frame2Offset+4+4])
	}
}

func TestStreamWriterExactMaxPacketFrameSplit(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 2, 0)

	// Create payload of exactly maximumPacketFrame
	payload := make([]byte, maximumPacketFrame)
	if err := writer.WritePacket(payload); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	// An exact maximumPacketFrame payload requires 2 packets:
	// 1 full packet (0xffffff) followed by an empty terminating packet (0x000000)
	if writer.PacketsWritten() != 2 {
		t.Fatalf("packets written = %d, want 2", writer.PacketsWritten())
	}
	if writer.Sequence() != 4 {
		t.Fatalf("writer sequence = %d, want 4", writer.Sequence())
	}

	data := buf.Bytes()
	frame2Offset := 4 + maximumPacketFrame
	if !bytes.Equal(data[frame2Offset:frame2Offset+4], []byte{0, 0, 0, 3}) {
		t.Fatalf("frame 2 terminating header = %v, want [0 0 0 3]", data[frame2Offset:frame2Offset+4])
	}
}

func TestStreamWriterWriteOK(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 1, 1024)

	if err := writer.WriteOK(10, 0, 2); err != nil {
		t.Fatalf("WriteOK: %v", err)
	}

	if writer.Sequence() != 2 {
		t.Fatalf("sequence = %d, want 2", writer.Sequence())
	}

	data := buf.Bytes()
	if len(data) < 4 {
		t.Fatalf("data length = %d, want >= 4", len(data))
	}
	payload := data[4:]
	if payload[0] != 0x00 {
		t.Fatalf("OK header byte = 0x%02x, want 0x00", payload[0])
	}
	// affected 10
	if payload[1] != 10 {
		t.Fatalf("affected = %d, want 10", payload[1])
	}
	// warnings at end: byte(2), byte(0)
	warnings := uint16(payload[len(payload)-2]) | uint16(payload[len(payload)-1])<<8
	if warnings != 2 {
		t.Fatalf("warnings = %d, want 2", warnings)
	}
}

// TestStreamWriterWriteOKEncodesLastInsertID proves that the OK packet carries
// the last insert ID as a length-encoded integer after the affected rows.
func TestStreamWriterWriteOKEncodesLastInsertID(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 1, 1024)

	if err := writer.WriteOK(2, 70000, 1); err != nil {
		t.Fatalf("WriteOK: %v", err)
	}

	want := []byte{0x00, 0x02, 0xfd, 0x70, 0x11, 0x01, 0x02, 0x00, 0x01, 0x00}
	if payload := buf.Bytes()[4:]; !bytes.Equal(payload, want) {
		t.Fatalf("OK payload = % x, want % x", payload, want)
	}
}

func TestStreamWriterWriteError(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 3, 1024)

	customErr := sqlFailure{code: 1048, state: "23000", message: "Column cannot be null"}
	if err := writer.WriteError(customErr); err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	if writer.Sequence() != 4 {
		t.Fatalf("sequence = %d, want 4", writer.Sequence())
	}

	payload := buf.Bytes()[4:]
	if payload[0] != 0xff {
		t.Fatalf("error header byte = 0x%02x, want 0xff", payload[0])
	}
	code := uint16(payload[1]) | uint16(payload[2])<<8
	if code != 1048 {
		t.Fatalf("error code = %d, want 1048", code)
	}
	if payload[3] != '#' {
		t.Fatalf("sqlstate marker = %c, want #", payload[3])
	}
	if string(payload[4:9]) != "23000" {
		t.Fatalf("sqlstate = %q, want 23000", string(payload[4:9]))
	}
	if string(payload[9:]) != "Column cannot be null" {
		t.Fatalf("error message = %q, want 'Column cannot be null'", string(payload[9:]))
	}

	// Test generic error defaults to 1064, 42000
	buf.Reset()
	if err := writer.WriteError(errors.New("bad syntax")); err != nil {
		t.Fatalf("WriteError generic: %v", err)
	}
	genericPayload := buf.Bytes()[4:]
	genericCode := uint16(genericPayload[1]) | uint16(genericPayload[2])<<8
	if genericCode != 1064 {
		t.Fatalf("generic code = %d, want 1064", genericCode)
	}
	if string(genericPayload[4:9]) != "42000" {
		t.Fatalf("generic sqlstate = %q, want 42000", string(genericPayload[4:9]))
	}
}

func TestStreamWriterWriteEOF(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 5, 1024)

	if err := writer.WriteEOF(4); err != nil {
		t.Fatalf("WriteEOF: %v", err)
	}

	if writer.Sequence() != 6 {
		t.Fatalf("sequence = %d, want 6", writer.Sequence())
	}

	payload := buf.Bytes()[4:]
	if payload[0] != 0xfe {
		t.Fatalf("EOF marker = 0x%02x, want 0xfe", payload[0])
	}
	warnings := uint16(payload[1]) | uint16(payload[2])<<8
	if warnings != 4 {
		t.Fatalf("EOF warnings = %d, want 4", warnings)
	}
}

func TestStreamWriterWriteHandshake(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 0, 1024)

	nonce := make([]byte, 20)
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}
	if err := writer.WriteHandshake("1.0.0", nonce, false, 42); err != nil {
		t.Fatalf("WriteHandshake: %v", err)
	}

	if writer.Sequence() != 1 {
		t.Fatalf("sequence = %d, want 1", writer.Sequence())
	}

	payload := buf.Bytes()[4:]
	if payload[0] != 0x0a {
		t.Fatalf("handshake protocol version = 0x%02x, want 0x0a", payload[0])
	}
}

func TestStreamWriterMismatchedTypeReturnsErrorBeforeWritingBytes(t *testing.T) {
	tests := []struct {
		name     string
		mode     ResultMode
		metadata columnMetadata
		value    string
	}{
		{
			name:     "binary long with non-numeric string",
			mode:     ResultModeBinary,
			metadata: columnMetadata{catalog: "def", name: "id", typ: mysqlTypeLong},
			value:    "invalid_int",
		},
		{
			name:     "binary double with non-numeric string",
			mode:     ResultModeBinary,
			metadata: columnMetadata{catalog: "def", name: "price", typ: mysqlTypeDouble},
			value:    "none",
		},
		{
			name:     "binary date with malformed date",
			mode:     ResultModeBinary,
			metadata: columnMetadata{catalog: "def", name: "created_at", typ: mysqlTypeDate},
			value:    "not-a-date",
		},
		{
			name:     "text bit exceeding width limit",
			mode:     ResultModeText,
			metadata: columnMetadata{catalog: "def", name: "flags", typ: mysqlTypeBit, length: 3},
			value:    "15", // 15 >= (1 << 3)
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			writer := NewStreamWriter(buf, 2, 1024)

			result := &queryResult{
				columns:  []string{tc.metadata.name},
				metadata: []columnMetadata{tc.metadata},
				rows:     [][]string{{tc.value}},
				nulls:    [][]bool{{false}},
			}

			err := writer.WriteResultSet(result, tc.mode)
			if err == nil {
				t.Fatalf("%s: expected validation error, got nil", tc.name)
			}
			if buf.Len() != 0 {
				t.Fatalf("%s: expected 0 bytes written to buffer on pre-flight error, got %d bytes: %x", tc.name, buf.Len(), buf.Bytes())
			}
			if writer.PacketsWritten() != 0 {
				t.Fatalf("%s: expected 0 packets written, got %d", tc.name, writer.PacketsWritten())
			}
			if writer.Sequence() != 2 {
				t.Fatalf("%s: expected sequence to remain 2, got %d", tc.name, writer.Sequence())
			}

			// Clean ERR packet can be written by the caller using writer.WriteError
			if err := writer.WriteError(err); err != nil {
				t.Fatalf("WriteError: %v", err)
			}
			if buf.Len() == 0 {
				t.Fatal("expected error packet bytes after WriteError")
			}
			if writer.Sequence() != 3 {
				t.Fatalf("sequence after WriteError = %d, want 3", writer.Sequence())
			}
		})
	}
}

func TestStreamWriterTextResultSet(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 0, 1024)

	longString := string(bytes.Repeat([]byte("a"), 260))
	result := &queryResult{
		columns: []string{"c_str", "c_empty", "c_long", "c_null", "c_bit"},
		metadata: []columnMetadata{
			{catalog: "def", name: "c_str", typ: mysqlTypeVarString},
			{catalog: "def", name: "c_empty", typ: mysqlTypeVarString},
			{catalog: "def", name: "c_long", typ: mysqlTypeVarString},
			{catalog: "def", name: "c_null", typ: mysqlTypeVarString},
			{catalog: "def", name: "c_bit", typ: mysqlTypeBit, length: 8},
		},
		rows:     [][]string{{"hello", "", longString, "ignored", "10"}},
		nulls:    [][]bool{{false, false, false, true, false}},
		warnings: 1,
	}

	if err := writer.WriteResultSet(result, ResultModeText); err != nil {
		t.Fatalf("WriteResultSet: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	// 1. Column count packet (seq 0)
	seq, payload, err := readPacket(reader, 1024)
	if err != nil || seq != 0 {
		t.Fatalf("col count packet: seq=%d err=%v", seq, err)
	}
	if len(payload) != 1 || payload[0] != 5 {
		t.Fatalf("col count payload = %v, want [5]", payload)
	}

	// 2..6 Column definitions (seq 1..5)
	for i := 0; i < 5; i++ {
		seq, _, err := readPacket(reader, 1024)
		if err != nil || seq != byte(i+1) {
			t.Fatalf("col %d def: seq=%d err=%v", i, seq, err)
		}
	}

	// 7. Column EOF packet (seq 6)
	seq, payload, err = readPacket(reader, 1024)
	if err != nil || seq != 6 || payload[0] != 0xfe {
		t.Fatalf("col EOF packet: seq=%d err=%v payload=%x", seq, err, payload)
	}

	// 8. Row packet (seq 7)
	seq, payload, err = readPacket(reader, 1024)
	if err != nil || seq != 7 {
		t.Fatalf("row packet: seq=%d err=%v", seq, err)
	}
	// Decode text row values
	offset := 0
	// col 0: "hello" -> len 5 + "hello"
	val, next, ok := readLengthEncoded(payload, offset)
	if !ok || string(val) != "hello" {
		t.Fatalf("col 0 = %q, want 'hello'", string(val))
	}
	offset = next
	// col 1: "" -> len 0
	val, next, ok = readLengthEncoded(payload, offset)
	if !ok || len(val) != 0 {
		t.Fatalf("col 1 = %q, want empty", string(val))
	}
	offset = next
	// col 2: longString -> 0xfc, len 260
	val, next, ok = readLengthEncoded(payload, offset)
	if !ok || string(val) != longString {
		t.Fatalf("col 2 len = %d, want %d", len(val), len(longString))
	}
	offset = next
	// col 3: null -> 0xfb
	if payload[offset] != 0xfb {
		t.Fatalf("col 3 null byte = 0x%02x, want 0xfb", payload[offset])
	}
	offset++
	// col 4: bit 10 (0x0a) -> len 1, 0x0a
	val, next, ok = readLengthEncoded(payload, offset)
	if !ok || len(val) != 1 || val[0] != 10 {
		t.Fatalf("col 4 bit = %v, want [10]", val)
	}

	// 9. Final EOF packet (seq 8) with warnings = 1
	seq, payload, err = readPacket(reader, 1024)
	if err != nil || seq != 8 || payload[0] != 0xfe {
		t.Fatalf("final EOF packet: seq=%d err=%v payload=%x", seq, err, payload)
	}
	warnings := uint16(payload[1]) | uint16(payload[2])<<8
	if warnings != 1 {
		t.Fatalf("final EOF warnings = %d, want 1", warnings)
	}
}

func TestStreamWriterBinaryResultSetNullBitmap(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 0, 1024)

	cols := make([]string, 10)
	meta := make([]columnMetadata, 10)
	row := make([]string, 10)
	nulls := make([]bool, 10)

	for i := range cols {
		cols[i] = fmt.Sprintf("c%d", i)
		meta[i] = columnMetadata{catalog: "def", name: cols[i], typ: mysqlTypeVarString}
		row[i] = fmt.Sprintf("val%d", i)
	}
	// Columns 0, 2, 7, 9 are NULL
	nulls[0] = true
	nulls[2] = true
	nulls[7] = true
	nulls[9] = true

	result := &queryResult{
		columns:  cols,
		metadata: meta,
		rows:     [][]string{row},
		nulls:    [][]bool{nulls},
	}

	if err := writer.WriteResultSet(result, ResultModeBinary); err != nil {
		t.Fatalf("WriteResultSet: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	// Skip col count (1) + 10 col defs + 1 col EOF = 12 packets
	for i := 0; i < 12; i++ {
		if _, _, err := readPacket(reader, 1024); err != nil {
			t.Fatalf("skip packet %d: %v", i, err)
		}
	}

	// Read binary row packet (seq 12)
	seq, payload, err := readPacket(reader, 1024)
	if err != nil || seq != 12 {
		t.Fatalf("row packet: seq=%d err=%v", seq, err)
	}

	// Header: 0x00
	if payload[0] != 0x00 {
		t.Fatalf("binary row header = 0x%02x, want 0x00", payload[0])
	}
	// Bitmap length for 10 columns: (10 + 9) / 8 = 2 bytes (offsets 1 and 2)
	// Check null bits:
	// col 0: bit (0+2)%8 = bit 2 of byte 1 + (0+2)/8 = byte 1 (1 << 2 = 0x04)
	// col 2: bit (2+2)%8 = bit 4 of byte 1 + (2+2)/8 = byte 1 (1 << 4 = 0x10)
	// col 7: bit (7+2)%8 = bit 1 of byte 1 + (7+2)/8 = byte 2 (1 << 1 = 0x02)
	// col 9: bit (9+2)%8 = bit 3 of byte 1 + (9+2)/8 = byte 2 (1 << 3 = 0x08)
	// byte 1 expected: 0x04 | 0x10 = 0x14
	// byte 2 expected: 0x02 | 0x08 = 0x0a
	if payload[1] != 0x14 {
		t.Fatalf("NULL bitmap byte 1 = 0x%02x, want 0x14", payload[1])
	}
	if payload[2] != 0x0a {
		t.Fatalf("NULL bitmap byte 2 = 0x%02x, want 0x0a", payload[2])
	}
}

func TestStreamWriterBinaryResultSetAllTypes(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 0, 4096)

	type testCol struct {
		meta  columnMetadata
		value string
	}

	cols := []testCol{
		{meta: columnMetadata{name: "c_tiny", typ: mysqlTypeTiny}, value: "-42"},
		{meta: columnMetadata{name: "c_utiny", typ: mysqlTypeTiny, flags: mysqlUnsignedFlag}, value: "200"},
		{meta: columnMetadata{name: "c_short", typ: mysqlTypeShort}, value: "-12345"},
		{meta: columnMetadata{name: "c_int24", typ: mysqlTypeInt24}, value: "-54321"},
		{meta: columnMetadata{name: "c_long", typ: mysqlTypeLong}, value: "-987654321"},
		{meta: columnMetadata{name: "c_longlong", typ: mysqlTypeLongLong}, value: "-98765432101234"},
		{meta: columnMetadata{name: "c_ulonglong", typ: mysqlTypeLongLong, flags: mysqlUnsignedFlag}, value: "18446744073709551615"},
		{meta: columnMetadata{name: "c_float", typ: mysqlTypeFloat}, value: "3.14"},
		{meta: columnMetadata{name: "c_double", typ: mysqlTypeDouble}, value: "2.718281828"},
		{meta: columnMetadata{name: "c_bit", typ: mysqlTypeBit, length: 8}, value: "170"}, // 0xAA
		{meta: columnMetadata{name: "c_date", typ: mysqlTypeDate}, value: "2026-09-25"},
		{meta: columnMetadata{name: "c_datetime", typ: mysqlTypeDatetime, decimals: 6}, value: "2026-09-25 10:15:30.123456"},
		{meta: columnMetadata{name: "c_timestamp", typ: mysqlTypeTimestamp}, value: "2026-09-25 10:15:30"},
		{meta: columnMetadata{name: "c_time", typ: mysqlTypeTime, decimals: 3}, value: "12:34:56.789"},
		{meta: columnMetadata{name: "c_year", typ: mysqlTypeYear}, value: "2026"},
		{meta: columnMetadata{name: "c_string", typ: mysqlTypeVarString}, value: "hello binary"},
	}

	names := make([]string, len(cols))
	metadata := make([]columnMetadata, len(cols))
	values := make([]string, len(cols))
	nulls := make([]bool, len(cols))

	for i, c := range cols {
		names[i] = c.meta.name
		metadata[i] = c.meta
		values[i] = c.value
		nulls[i] = false
	}

	result := &queryResult{
		columns:  names,
		metadata: metadata,
		rows:     [][]string{values},
		nulls:    [][]bool{nulls},
	}

	if err := writer.WriteResultSet(result, ResultModeBinary); err != nil {
		t.Fatalf("WriteResultSet: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	// Skip col count + defs + EOF
	for i := 0; i < len(cols)+2; i++ {
		if _, _, err := readPacket(reader, 4096); err != nil {
			t.Fatalf("skip packet %d: %v", i, err)
		}
	}

	// Read binary row
	seq, payload, err := readPacket(reader, 4096)
	if err != nil {
		t.Fatalf("read binary row: %v", err)
	}
	if seq != byte(len(cols)+2) {
		t.Fatalf("row sequence = %d, want %d", seq, len(cols)+2)
	}

	// Row must start with 0x00
	if payload[0] != 0x00 {
		t.Fatalf("binary row header = 0x%02x, want 0x00", payload[0])
	}
	// Null bitmap length: 1 + (16 + 9) / 8 = 4 bytes (header byte + 3 bitmap bytes)
	nullBitmapLen := 1 + (len(cols)+9)/8
	rowPayload := payload[nullBitmapLen:]
	if len(rowPayload) == 0 {
		t.Fatal("empty row values payload")
	}

	// Read final EOF packet
	eofSeq, eofPayload, err := readPacket(reader, 4096)
	if err != nil || eofSeq != seq+1 || eofPayload[0] != 0xfe {
		t.Fatalf("final EOF packet: seq=%d payload=%x err=%v", eofSeq, eofPayload, err)
	}
}

func TestStreamWriterPreparedMetadata(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewStreamWriter(buf, 1, 1024)

	metadata := []columnMetadata{
		{catalog: "def", name: "col1", typ: mysqlTypeLong},
	}

	if err := writer.WritePreparedMetadata(100, 2, metadata); err != nil {
		t.Fatalf("WritePreparedMetadata: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	// 1. Prepared OK response packet (seq 1)
	seq, payload, err := readPacket(reader, 1024)
	if err != nil || seq != 1 {
		t.Fatalf("prepared OK: seq=%d err=%v", seq, err)
	}
	if payload[0] != 0x00 {
		t.Fatalf("prepared OK prefix = 0x%02x, want 0x00", payload[0])
	}
	stmtID := binary.LittleEndian.Uint32(payload[1:5])
	if stmtID != 100 {
		t.Fatalf("stmtID = %d, want 100", stmtID)
	}
	colCount := binary.LittleEndian.Uint16(payload[5:7])
	if colCount != 1 {
		t.Fatalf("colCount = %d, want 1", colCount)
	}
	paramCount := binary.LittleEndian.Uint16(payload[7:9])
	if paramCount != 2 {
		t.Fatalf("paramCount = %d, want 2", paramCount)
	}

	// 2. Param 1 definition (seq 2)
	seq, _, err = readPacket(reader, 1024)
	if err != nil || seq != 2 {
		t.Fatalf("param 1: seq=%d err=%v", seq, err)
	}

	// 3. Param 2 definition (seq 3)
	seq, _, err = readPacket(reader, 1024)
	if err != nil || seq != 3 {
		t.Fatalf("param 2: seq=%d err=%v", seq, err)
	}

	// 4. Params EOF packet (seq 4)
	seq, payload, err = readPacket(reader, 1024)
	if err != nil || seq != 4 || payload[0] != 0xfe {
		t.Fatalf("params EOF: seq=%d err=%v payload=%x", seq, err, payload)
	}

	// 5. Column 1 definition (seq 5)
	seq, _, err = readPacket(reader, 1024)
	if err != nil || seq != 5 {
		t.Fatalf("col 1: seq=%d err=%v", seq, err)
	}

	// 6. Columns EOF packet (seq 6)
	seq, payload, err = readPacket(reader, 1024)
	if err != nil || seq != 6 || payload[0] != 0xfe {
		t.Fatalf("columns EOF: seq=%d err=%v payload=%x", seq, err, payload)
	}

	if writer.Sequence() != 7 {
		t.Fatalf("writer sequence = %d, want 7", writer.Sequence())
	}
}
