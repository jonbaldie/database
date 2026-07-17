package mysql

import (
	"encoding/binary"
	"math"
	"net"
	"strconv"
)

func writeResult(connection net.Conn, sequence byte, result *queryResult, maximum int64) error {
	writer := newResultWriter(connection, sequence, maximum)
	if err := writer.writeColumns(result.columns, result.metadata); err != nil {
		return err
	}
	if err := writer.writeTextRows(result.rows, result.nulls); err != nil {
		return err
	}
	return writer.writeEOF()
}

func writeBinaryResult(connection net.Conn, sequence byte, result *queryResult, maximum int64) error {
	writer := newResultWriter(connection, sequence, maximum)
	if err := writer.writeColumns(result.columns, result.metadata); err != nil {
		return err
	}
	if err := writer.writeBinaryRows(result.rows, result.nulls); err != nil {
		return err
	}
	return writer.writeEOF()
}

type resultWriter struct {
	connection  net.Conn
	sequence    byte
	maximum     int64
	definitions []columnMetadata
}

func newResultWriter(connection net.Conn, sequence byte, maximum int64) *resultWriter {
	return &resultWriter{connection: connection, sequence: sequence, maximum: maximum}
}

func (w *resultWriter) writeColumns(columns []string, metadata []columnMetadata) error {
	if err := w.write(lengthEncodedInt(len(columns))); err != nil {
		return err
	}
	w.definitions = resultColumnDefinitions(columns, metadata)
	for _, definition := range w.definitions {
		if err := w.write(columnDefinition(definition)); err != nil {
			return err
		}
	}
	return w.writeEOF()
}

func (w *resultWriter) writeTextRows(rows [][]string, nulls [][]bool) error {
	for rowIndex, row := range rows {
		if err := w.write(textRow(row, rowIndex, nulls)); err != nil {
			return err
		}
	}
	return nil
}

func (w *resultWriter) writeBinaryRows(rows [][]string, nulls [][]bool) error {
	for rowIndex, row := range rows {
		payload, err := binaryRow(row, rowIndex, nulls, w.definitions)
		if err != nil {
			return err
		}
		if err := w.write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (w *resultWriter) writeEOF() error { return w.write(eofPacket()) }

func (w *resultWriter) write(payload []byte) error {
	if err := writeBoundedPacket(w.connection, w.sequence, payload, w.maximum); err != nil {
		return err
	}
	w.sequence = nextPacketSequence(w.sequence, payload)
	return nil
}

func resultColumnDefinitions(columns []string, metadata []columnMetadata) []columnMetadata {
	definitions := make([]columnMetadata, len(columns))
	for index, name := range columns {
		definitions[index] = resultColumnDefinition(name, index, metadata)
	}
	return definitions
}

func resultColumnDefinition(name string, index int, metadata []columnMetadata) columnMetadata {
	if index < len(metadata) {
		return metadata[index]
	}
	return columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB4GeneralCI, typ: mysqlTypeVarString}
}

func textRow(row []string, rowIndex int, nulls [][]bool) []byte {
	payload := make([]byte, 0)
	for columnIndex, value := range row {
		if resultValueIsNull(rowIndex, columnIndex, nulls) {
			payload = append(payload, 0xfb)
			continue
		}
		payload = append(payload, lengthEncodedString(value)...)
	}
	return payload
}

func resultValueIsNull(rowIndex, columnIndex int, nulls [][]bool) bool {
	return rowIndex < len(nulls) && columnIndex < len(nulls[rowIndex]) && nulls[rowIndex][columnIndex]
}

func binaryRow(row []string, rowIndex int, nulls [][]bool, metadata []columnMetadata) ([]byte, error) {
	payload := make([]byte, 1+(len(metadata)+9)/8)
	for index, value := range row {
		if resultValueIsNull(rowIndex, index, nulls) {
			setBinaryNull(payload, index)
			continue
		}
		encoded, err := encodeBinaryValue(value, resultColumnDefinition("", index, metadata))
		if err != nil {
			return nil, err
		}
		payload = append(payload, encoded...)
	}
	return payload, nil
}

func setBinaryNull(payload []byte, index int) {
	payload[1+(index+2)/8] |= 1 << uint((index+2)%8)
}

func encodeBinaryValue(value string, definition columnMetadata) ([]byte, error) {
	switch definition.typ {
	case mysqlTypeLongLong:
		return encodeBinaryLongLong(value, definition.flags&mysqlUnsignedFlag != 0)
	case mysqlTypeLong:
		return encodeBinaryLong(value)
	case mysqlTypeDouble:
		return encodeBinaryDouble(value)
	default:
		return lengthEncodedString(value), nil
	}
}

func encodeBinaryLongLong(value string, unsigned bool) ([]byte, error) {
	encoded := make([]byte, 8)
	if unsigned {
		parsed, err := strconv.ParseUint(value, 10, 64)
		binary.LittleEndian.PutUint64(encoded, parsed)
		return encoded, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	binary.LittleEndian.PutUint64(encoded, uint64(parsed))
	return encoded, err
}

func encodeBinaryLong(value string) ([]byte, error) {
	parsed, err := strconv.ParseInt(value, 10, 32)
	encoded := make([]byte, 4)
	binary.LittleEndian.PutUint32(encoded, uint32(parsed))
	return encoded, err
}

func encodeBinaryDouble(value string) ([]byte, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	encoded := make([]byte, 8)
	binary.LittleEndian.PutUint64(encoded, math.Float64bits(parsed))
	return encoded, err
}

func columnDefinition(definition columnMetadata) []byte {
	payload := append(lengthEncodedString(definition.catalog), lengthEncodedString(definition.schema)...)
	payload = append(payload, lengthEncodedString(definition.table)...)
	payload = append(payload, lengthEncodedString(definition.originalTable)...)
	payload = append(payload, lengthEncodedString(definition.name)...)
	payload = append(payload, lengthEncodedString(definition.originalName)...)
	payload = append(payload, 0x0c, byte(definition.characterSet), byte(definition.characterSet>>8), byte(definition.length), byte(definition.length>>8), byte(definition.length>>16), byte(definition.length>>24), definition.typ, byte(definition.flags), byte(definition.flags>>8), definition.decimals, 0, 0)
	return payload
}

func eofPacket() []byte { return []byte{0xfe, 0, 0, 2, 0} }

func lengthEncodedInt(value int) []byte {
	if value < 251 {
		return []byte{byte(value)}
	}
	if value <= 0xffff {
		return []byte{0xfc, byte(value), byte(value >> 8)}
	}
	if value <= 0xffffff {
		return []byte{0xfd, byte(value), byte(value >> 8), byte(value >> 16)}
	}
	return []byte{0xfe, byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24), byte(value >> 32), byte(value >> 40), byte(value >> 48), byte(value >> 56)}
}

func lengthEncodedString(value string) []byte {
	return append(lengthEncodedInt(len(value)), []byte(value)...)
}

func readLengthEncoded(payload []byte, offset int) ([]byte, int, bool) {
	length, next, null, ok := lengthEncodedSize(payload, offset)
	if !ok || null {
		return nil, next, ok
	}
	if next+length > len(payload) {
		return nil, next, false
	}
	return payload[next : next+length], next + length, true
}

func lengthEncodedSize(payload []byte, offset int) (int, int, bool, bool) {
	if offset >= len(payload) {
		return 0, offset, false, false
	}
	prefix := payload[offset]
	offset++
	switch prefix {
	case 0xfb:
		return 0, offset, true, true
	case 0xfc:
		return twoByteLength(payload, offset)
	case 0xfd:
		return threeByteLength(payload, offset)
	case 0xfe:
		return eightByteLength(payload, offset)
	default:
		return int(prefix), offset, false, true
	}
}

func twoByteLength(payload []byte, offset int) (int, int, bool, bool) {
	if offset+2 > len(payload) {
		return 0, offset, false, false
	}
	return int(binary.LittleEndian.Uint16(payload[offset : offset+2])), offset + 2, false, true
}

func threeByteLength(payload []byte, offset int) (int, int, bool, bool) {
	if offset+3 > len(payload) {
		return 0, offset, false, false
	}
	length := int(payload[offset]) | int(payload[offset+1])<<8 | int(payload[offset+2])<<16
	return length, offset + 3, false, true
}

func eightByteLength(payload []byte, offset int) (int, int, bool, bool) {
	if offset+8 > len(payload) {
		return 0, offset, false, false
	}
	length := binary.LittleEndian.Uint64(payload[offset : offset+8])
	next := offset + 8
	if length > uint64(len(payload)-next) {
		return 0, next, false, false
	}
	return int(length), next, false, true
}

func readNullString(payload []byte, offset int) (string, int, bool) {
	value, next := readNullBytes(payload, offset)
	if value == nil {
		return "", offset, false
	}
	return string(value), next, true
}

func readNullBytes(payload []byte, offset int) ([]byte, int) {
	end := offset
	payloadLength := len(payload)
	for end < payloadLength && payload[end] != 0 {
		end++
	}
	if end >= payloadLength {
		return nil, offset
	}
	return payload[offset:end], end + 1
}
