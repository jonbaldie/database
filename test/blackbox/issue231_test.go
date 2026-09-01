package blackbox_test

import (
	"bytes"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue231BitResultWireBytesAgreeForTextAndPrepared(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE bits (b1 BIT(1), b4 BIT(4), b8 BIT(8), b9 BIT(9), b63 BIT(63), b64 BIT(64))",
		"INSERT INTO bits VALUES (0, 0, 0, 0, 0, 0)",
		"INSERT INTO bits VALUES (1, 10, 128, 256, 4611686018427387904, 9223372036854775808)",
		"INSERT INTO bits VALUES (1, 15, 255, 511, 9223372036854775807, 18446744073709551615)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	query := "SELECT b1, b4, b8, b9, b63, b64 FROM bits ORDER BY b1, b4"
	textResult := issue231QueryRaw(t, client, query)
	assertIssue231BitMetadata(t, textResult.result.metadata, []uint32{1, 4, 8, 9, 63, 64})

	for rowIndex, packet := range textResult.rows {
		fields := issue231TextFields(t, packet, len(textResult.result.metadata))
		want := issue231ExpectedFields(rowIndex)
		if !equalByteFields(fields, want) {
			t.Fatalf("text row %d fields = %x, want %x", rowIndex, fields, want)
		}
	}

	prepared := client.prepare(query)
	if prepared.err != "" {
		t.Fatalf("prepare BIT result: %#v", prepared)
	}
	preparedResult := issue231ExecutePreparedRaw(t, client, prepared.id)
	assertIssue231BitMetadata(t, preparedResult.result.metadata, []uint32{1, 4, 8, 9, 63, 64})
	if len(preparedResult.rows) != len(textResult.rows) {
		t.Fatalf("prepared row count = %d, want %d", len(preparedResult.rows), len(textResult.rows))
	}
	for rowIndex, packet := range preparedResult.rows {
		fields := issue231BinaryFields(t, packet, preparedResult.result.metadata)
		want := issue231ExpectedFields(rowIndex)
		if !equalByteFields(fields, want) {
			t.Fatalf("prepared row %d fields = %x, want %x", rowIndex, fields, want)
		}
	}
	client.closePrepared(prepared.id)
}

type issue231RawResult struct {
	result wireResult
	rows   [][]byte
}

func issue231QueryRaw(t *testing.T, client *wireClient, query string) issue231RawResult {
	t.Helper()
	writeWirePacket(t, client.conn, 0, append([]byte{0x03}, query...))
	return issue231ReadRawResult(t, client)
}

func issue231ExecutePreparedRaw(t *testing.T, client *wireClient, id uint32) issue231RawResult {
	t.Helper()
	payload := []byte{0x17, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), 0, 1, 0, 0, 0}
	writeWirePacket(t, client.conn, 0, payload)
	return issue231ReadRawResult(t, client)
}

func issue231ReadRawResult(t *testing.T, client *wireClient) issue231RawResult {
	t.Helper()
	result, complete := client.readResultHeader()
	if complete {
		if result.err != "" {
			t.Fatalf("raw result header: %#v", result)
		}
		return issue231RawResult{result: result}
	}
	rows := make([][]byte, 0)
	for {
		packet := readWirePacket(t, client.conn)
		if len(packet) == 0 {
			t.Fatal("empty raw result row")
		}
		if packet[0] == 0xfe && len(packet) < 9 {
			return issue231RawResult{result: result, rows: rows}
		}
		rows = append(rows, packet)
	}
}

func assertIssue231BitMetadata(t *testing.T, metadata []wireColumn, widths []uint32) {
	t.Helper()
	if len(metadata) != len(widths) {
		t.Fatalf("BIT metadata count = %d, want %d", len(metadata), len(widths))
	}
	for index, column := range metadata {
		if column.typ != 0x10 || column.characterSet != 63 || column.length != widths[index] {
			t.Fatalf("BIT metadata %d = %#v, want type 0x10 charset 63 length %d", index, column, widths[index])
		}
	}
}

func issue231TextFields(t *testing.T, packet []byte, count int) [][]byte {
	t.Helper()
	fields := make([][]byte, count)
	offset := 0
	for index := range fields {
		value, next, ok := issue231ReadLengthBytes(packet, offset)
		if !ok {
			t.Fatalf("malformed text row %x at column %d", packet, index)
		}
		fields[index], offset = value, next
	}
	if offset != len(packet) {
		t.Fatalf("text row has trailing bytes %x", packet[offset:])
	}
	return fields
}

func issue231BinaryFields(t *testing.T, packet []byte, metadata []wireColumn) [][]byte {
	t.Helper()
	nullBytes := (len(metadata) + 9) / 8
	if len(packet) < 1+nullBytes || packet[0] != 0 {
		t.Fatalf("malformed binary row %x", packet)
	}
	fields := make([][]byte, len(metadata))
	offset := 1 + nullBytes
	for index, column := range metadata {
		if packet[1+(index+2)/8]&(1<<uint((index+2)%8)) != 0 {
			continue
		}
		switch column.typ {
		case 0x03:
			if offset+4 > len(packet) {
				t.Fatalf("truncated INT binary row %x", packet)
			}
			fields[index], offset = packet[offset:offset+4], offset+4
		case 0x08:
			if offset+8 > len(packet) {
				t.Fatalf("truncated BIGINT binary row %x", packet)
			}
			fields[index], offset = packet[offset:offset+8], offset+8
		case 0x05:
			if offset+8 > len(packet) {
				t.Fatalf("truncated DOUBLE binary row %x", packet)
			}
			fields[index], offset = packet[offset:offset+8], offset+8
		default:
			value, next, ok := issue231ReadLengthBytes(packet, offset)
			if !ok {
				t.Fatalf("malformed binary row %x at column %d", packet, index)
			}
			fields[index], offset = value, next
		}
	}
	if offset != len(packet) {
		t.Fatalf("binary row has trailing bytes %x", packet[offset:])
	}
	return fields
}

func issue231ReadLengthBytes(payload []byte, offset int) ([]byte, int, bool) {
	if offset < len(payload) && payload[offset] == 0xfb {
		return nil, offset + 1, true
	}
	length, next, ok := readLengthInt(payload, offset)
	if !ok || next+length > len(payload) {
		return nil, next, false
	}
	return payload[next : next+length], next + length, true
}

func issue231ExpectedFields(row int) [][]byte {
	switch row {
	case 0:
		return [][]byte{{0}, {0}, {0}, {0, 0}, {0, 0, 0, 0, 0, 0, 0, 0}, {0, 0, 0, 0, 0, 0, 0, 0}}
	case 1:
		return [][]byte{{1}, {0x0a}, {0x80}, {1, 0}, {0x40, 0, 0, 0, 0, 0, 0, 0}, {0x80, 0, 0, 0, 0, 0, 0, 0}}
	default:
		return [][]byte{{1}, {0x0f}, {0xff}, {1, 0xff}, {0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	}
}

func equalByteFields(got, want [][]byte) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if !bytes.Equal(got[index], want[index]) {
			return false
		}
	}
	return true
}
