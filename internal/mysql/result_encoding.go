package mysql

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
)

type queryRowStream func(func([]string, []bool) error) error

func writeResult(connection net.Conn, sequence byte, result *queryResult, maximum int64) error {
	writer := newResultWriter(connection, sequence, maximum)
	if err := writer.writeColumns(result.columns, result.metadata); err != nil {
		return err
	}
	if err := writer.writeTextResultRows(result); err != nil {
		return writer.writeStreamError(err)
	}
	return writer.writeEOF(result.warnings)
}

func writeBinaryResult(connection net.Conn, sequence byte, result *queryResult, maximum int64) error {
	writer := newResultWriter(connection, sequence, maximum)
	if err := writer.writeColumns(result.columns, result.metadata); err != nil {
		return err
	}
	if err := writer.writeBinaryResultRows(result); err != nil {
		return writer.writeStreamError(err)
	}
	return writer.writeEOF(result.warnings)
}

func (w *resultWriter) writeTextResultRows(result *queryResult) error {
	if result.stream == nil {
		return w.writeTextRows(result.rows, result.nulls)
	}
	return result.stream(func(row []string, nulls []bool) error {
		payload, err := textRowWithDefinitions(row, 0, [][]bool{nulls}, w.definitions)
		if err != nil {
			return err
		}
		return w.write(payload)
	})
}

func (w *resultWriter) writeBinaryResultRows(result *queryResult) error {
	if result.stream == nil {
		return w.writeBinaryRows(result.rows, result.nulls)
	}
	return result.stream(func(row []string, nulls []bool) error {
		payload, err := binaryRow(row, 0, [][]bool{nulls}, w.definitions)
		if err != nil {
			return err
		}
		return w.write(payload)
	})
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
		payload, err := textRowWithDefinitions(row, rowIndex, nulls, w.definitions)
		if err != nil {
			return err
		}
		if err := w.write(payload); err != nil {
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

func (w *resultWriter) writeEOF(warnings ...uint16) error { return w.write(eofPacket(warnings...)) }

func (w *resultWriter) writeStreamError(err error) error {
	var failure sqlFailure
	if !errors.As(err, &failure) {
		return err
	}
	return w.write(mysqlError(err))
}

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
	return columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetUTF8MB40900AICI, typ: mysqlTypeVarString}
}

func textRow(row []string, rowIndex int, nulls [][]bool) []byte {
	payload, _ := textRowWithDefinitions(row, rowIndex, nulls, nil)
	return payload
}

func textRowWithDefinitions(row []string, rowIndex int, nulls [][]bool, definitions []columnMetadata) ([]byte, error) {
	payload := make([]byte, 0)
	for columnIndex, value := range row {
		if resultValueIsNull(rowIndex, columnIndex, nulls) {
			payload = append(payload, 0xfb)
			continue
		}
		encoded, err := encodeTextValue(value, resultColumnDefinition("", columnIndex, definitions))
		if err != nil {
			return nil, err
		}
		payload = append(payload, encoded...)
	}
	return payload, nil
}

func encodeTextValue(value string, definition columnMetadata) ([]byte, error) {
	if definition.typ != mysqlTypeBit {
		return lengthEncodedString(value), nil
	}
	encoded, err := encodeBitResultValue(value, definition.length)
	if err != nil {
		return nil, err
	}
	return lengthEncodedBytes(encoded), nil
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
	case mysqlTypeBit:
		encoded, err := encodeBitResultValue(value, definition.length)
		if err != nil {
			return nil, err
		}
		return lengthEncodedBytes(encoded), nil
	case mysqlTypeDate, mysqlTypeDatetime, mysqlTypeTimestamp, mysqlTypeTime:
		return encodeBinaryTemporal(value, definition.typ)
	case mysqlTypeYear:
		return encodeBinaryYear(value)
	default:
		return lengthEncodedString(value), nil
	}
}

func encodeBinaryTemporal(value string, wire byte) ([]byte, error) {
	switch wire {
	case mysqlTypeDate:
		return encodeBinaryDate(value)
	case mysqlTypeDatetime, mysqlTypeTimestamp:
		return encodeBinaryDatetime(value)
	case mysqlTypeTime:
		return encodeBinaryTime(value)
	default:
		return nil, fmt.Errorf("unsupported binary temporal type %#x", wire)
	}
}

func encodeBinaryDate(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "0000-00-00" {
		return []byte{0}, nil
	}
	year, month, day, err := parseBinaryDate(value)
	if err != nil {
		return nil, err
	}
	return []byte{4, byte(year), byte(year >> 8), byte(month), byte(day)}, nil
}

func encodeBinaryDatetime(value string) ([]byte, error) {
	parts, err := parseBinaryDatetime(value)
	if err != nil {
		return nil, err
	}
	if parts.zero {
		return []byte{0}, nil
	}
	return binaryDatetimePayload(parts), nil
}

func encodeBinaryTime(value string) ([]byte, error) {
	parts, err := parseBinaryTime(value)
	if err != nil {
		return nil, err
	}
	if parts.zero {
		return []byte{0}, nil
	}
	return binaryTimePayload(parts), nil
}

type binaryDatetimeParts struct {
	year, month, day     int
	hour, minute, second int
	micros               int
	zero                 bool
}

func parseBinaryDatetime(value string) (binaryDatetimeParts, error) {
	value = strings.TrimSpace(value)
	if value == "0000-00-00 00:00:00" || value == "0000-00-00 00:00:00.000000" {
		return binaryDatetimeParts{zero: true}, nil
	}
	datePart, clockPart, found := strings.Cut(value, " ")
	if !found {
		return binaryDatetimeParts{}, fmt.Errorf("malformed binary datetime %q", value)
	}
	year, month, day, err := parseBinaryDate(datePart)
	if err != nil {
		return binaryDatetimeParts{}, err
	}
	clock, fraction, err := splitTemporalFraction(temporalType{precision: 6}, clockPart, "DATETIME", "result", 0)
	if err != nil {
		return binaryDatetimeParts{}, err
	}
	hour, minute, second, err := parseClock(clock, "DATETIME", "result", value, 0)
	if err != nil {
		return binaryDatetimeParts{}, err
	}
	micros, err := binaryTemporalMicros(fraction)
	if err != nil {
		return binaryDatetimeParts{}, err
	}
	return binaryDatetimeParts{year: year, month: month, day: day, hour: hour, minute: minute, second: second, micros: micros}, nil
}

func binaryDatetimePayload(parts binaryDatetimeParts) []byte {
	body := []byte{4, byte(parts.year), byte(parts.year >> 8), byte(parts.month), byte(parts.day)}
	if parts.hour == 0 && parts.minute == 0 && parts.second == 0 && parts.micros == 0 {
		return body
	}
	body[0] = 7
	body = append(body, byte(parts.hour), byte(parts.minute), byte(parts.second))
	if parts.micros == 0 {
		return body
	}
	body[0] = 11
	microseconds := make([]byte, 4)
	binary.LittleEndian.PutUint32(microseconds, uint32(parts.micros))
	return append(body, microseconds...)
}

type binaryTimeParts struct {
	negative           bool
	days, hour, minute int
	second, micros     int
	zero               bool
}

func parseBinaryTime(value string) (binaryTimeParts, error) {
	value = strings.TrimSpace(value)
	negative := strings.HasPrefix(value, "-")
	body := strings.TrimPrefix(value, "-")
	clock, fraction, err := splitTemporalFraction(temporalType{precision: 6}, body, "TIME", "result", 0)
	if err != nil {
		return binaryTimeParts{}, err
	}
	hours, minutes, seconds, ok := parseTimeClock(clock)
	if !validBinaryTimeClock(hours, minutes, seconds, ok) {
		return binaryTimeParts{}, fmt.Errorf("malformed binary time %q", value)
	}
	micros, err := binaryTemporalMicros(fraction)
	if err != nil {
		return binaryTimeParts{}, err
	}
	days, hour := hours/24, hours%24
	return binaryTimeParts{negative: negative, days: days, hour: hour, minute: minutes, second: seconds, micros: micros, zero: days == 0 && hour == 0 && minutes == 0 && seconds == 0 && micros == 0}, nil
}

func validBinaryTimeClock(hours, minutes, seconds int, parsed bool) bool {
	return parsed && minutes <= 59 && seconds <= 59 && hours <= 838
}

func binaryTimePayload(parts binaryTimeParts) []byte {
	body := []byte{8, 0, byte(parts.days), byte(parts.days >> 8), byte(parts.days >> 16), byte(parts.days >> 24), byte(parts.hour), byte(parts.minute), byte(parts.second)}
	if parts.negative {
		body[1] = 1
	}
	if parts.micros == 0 {
		return body
	}
	body[0] = 12
	microseconds := make([]byte, 4)
	binary.LittleEndian.PutUint32(microseconds, uint32(parts.micros))
	return append(body, microseconds...)
}

func encodeBinaryYear(value string) ([]byte, error) {
	year, ok := parseFixedDigits(strings.TrimSpace(value))
	if !ok || year < 0 || year > 65535 {
		return nil, fmt.Errorf("malformed binary year %q", value)
	}
	return []byte{byte(year), byte(year >> 8)}, nil
}

func parseBinaryDate(value string) (int, int, int, error) {
	year, month, day, err := parseCalendarDate(value, "result", 0)
	if err != nil {
		return 0, 0, 0, err
	}
	return year, month, day, nil
}

func binaryTemporalMicros(fraction string) (int, error) {
	if fraction == "" {
		return 0, nil
	}
	micros, err := strconv.Atoi(strings.TrimPrefix(fraction, "."))
	if err != nil || micros < 0 || micros > 999999 {
		return 0, fmt.Errorf("malformed binary temporal fraction %q", fraction)
	}
	return micros, nil
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

func encodeBitResultValue(value string, width uint32) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, fmt.Errorf("unsupported BIT result width %d", width)
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("malformed BIT result %q", value)
	}
	if width < 64 && parsed >= uint64(1)<<width {
		return nil, fmt.Errorf("BIT result %q exceeds width %d", value, width)
	}
	byteCount := int((width + 7) / 8)
	encoded := make([]byte, byteCount)
	for index := range encoded {
		shift := uint((byteCount - 1 - index) * 8)
		encoded[index] = byte(parsed >> shift)
	}
	return encoded, nil
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

func eofPacket(warnings ...uint16) []byte {
	count := uint16(0)
	if len(warnings) > 0 {
		count = warnings[0]
	}
	return []byte{0xfe, byte(count), byte(count >> 8), 2, 0}
}

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

func lengthEncodedUint(value uint64) []byte {
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
	return lengthEncodedBytes([]byte(value))
}

func lengthEncodedBytes(value []byte) []byte {
	return append(lengthEncodedInt(len(value)), value...)
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
