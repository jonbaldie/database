package mysql

import (
	"errors"
	"fmt"
	"io"
	"strconv"
)

const maximumPacketFrame = (1 << 24) - 1

// ResultMode specifies whether a query result is encoded in MySQL text or binary protocol.
type ResultMode int

const (
	// ResultModeText indicates text-protocol result row encoding.
	ResultModeText ResultMode = iota
	// ResultModeBinary indicates prepared-statement binary-protocol row encoding.
	ResultModeBinary
)

// StreamWriter frames and writes MySQL wire-protocol packets to an io.Writer.
type StreamWriter struct {
	writer         io.Writer
	sequence       byte
	maxPacket      int64
	packetsWritten int
}

// NewStreamWriter initializes a StreamWriter with the target writer, starting packet sequence, and maximum packet size limit.
func NewStreamWriter(w io.Writer, sequence byte, maxPacket int64) *StreamWriter {
	return &StreamWriter{
		writer:    w,
		sequence:  sequence,
		maxPacket: maxPacket,
	}
}

// Sequence returns the next packet sequence number.
func (w *StreamWriter) Sequence() byte {
	return w.sequence
}

// PacketsWritten returns the total number of wire packets emitted.
func (w *StreamWriter) PacketsWritten() int {
	return w.packetsWritten
}

// WritePacket encodes a payload with 4-byte MySQL packet headers, splitting across maximumPacketFrame boundaries.
func (w *StreamWriter) WritePacket(payload []byte) error {
	if w.maxPacket > 0 && int64(len(payload)) > w.maxPacket {
		return errors.New("packet exceeds configured maximum size")
	}
	return w.writeRawPacket(payload)
}

func (w *StreamWriter) writeRawPacket(payload []byte) error {
	for {
		length := len(payload)
		if length > maximumPacketFrame {
			length = maximumPacketFrame
		}
		header := []byte{byte(length), byte(length >> 8), byte(length >> 16), w.sequence}
		if _, err := w.writer.Write(header); err != nil {
			return err
		}
		if _, err := w.writer.Write(payload[:length]); err != nil {
			return err
		}
		w.packetsWritten++
		w.sequence++
		payload = payload[length:]
		if length < maximumPacketFrame {
			return nil
		}
	}
}

// WriteOK encodes an OK packet with affected row count and warning count.
func (w *StreamWriter) WriteOK(affected uint64, warnings uint16) error {
	return w.WritePacket(okPacketWithWarnings(affected, warnings))
}

// WriteError encodes an error packet from an error or sqlFailure.
func (w *StreamWriter) WriteError(err error) error {
	return w.WritePacket(mysqlError(err))
}

// WriteEOF encodes an EOF packet with optional warning count.
func (w *StreamWriter) WriteEOF(warnings ...uint16) error {
	return w.WritePacket(eofPacket(warnings...))
}

// WriteHandshake encodes an initial server handshake packet.
func (w *StreamWriter) WriteHandshake(version string, nonce []byte, tlsEnabled bool, connectionID uint32) error {
	return w.WritePacket(handshake(version, nonce, tlsEnabled, connectionID))
}

// WritePreparedMetadata streams the preparation response packets for a prepared statement.
func (w *StreamWriter) WritePreparedMetadata(id uint32, parameters int, metadata []columnMetadata) error {
	return writePreparedMetadataPackets(w, id, parameters, metadata)
}

// WriteResultSet streams a complete query result set in text or binary mode, validating column types first.
func (w *StreamWriter) WriteResultSet(result *queryResult, mode ResultMode) error {
	definitions := resultColumnDefinitions(result.columns, result.metadata)
	if err := validateResultSet(result, definitions, mode); err != nil {
		return err
	}
	if err := writeColumns(w, result.columns, definitions); err != nil {
		return err
	}
	if err := writeRows(w, result, definitions, mode); err != nil {
		return writeStreamError(w, err)
	}
	return w.WriteEOF(result.warnings)
}

func writeResult(connection io.Writer, sequence byte, result *queryResult, maximum int64) error {
	writer := NewStreamWriter(connection, sequence, maximum)
	if err := writer.WriteResultSet(result, ResultModeText); err != nil {
		if writer.PacketsWritten() == 0 {
			return writer.WriteError(err)
		}
		return err
	}
	return nil
}

func writePreparedMetadataPackets(w *StreamWriter, id uint32, parameters int, metadata []columnMetadata) error {
	response := []byte{0x00, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), byte(len(metadata)), 0, byte(parameters), byte(parameters >> 8), 0, 0, 0}
	if err := w.WritePacket(response); err != nil {
		return err
	}
	if parameters > 0 {
		for i := 0; i < parameters; i++ {
			payload := columnDefinition(preparedParameterMetadata(i))
			if err := w.WritePacket(payload); err != nil {
				return err
			}
		}
		if err := w.WritePacket(eofPacket()); err != nil {
			return err
		}
	}
	for _, definition := range metadata {
		payload := columnDefinition(definition)
		if err := w.WritePacket(payload); err != nil {
			return err
		}
	}
	if len(metadata) > 0 {
		return w.WritePacket(eofPacket())
	}
	return nil
}

func writeColumns(w *StreamWriter, columns []string, definitions []columnMetadata) error {
	if err := w.WritePacket(lengthEncodedInt(len(columns))); err != nil {
		return err
	}
	for _, definition := range definitions {
		if err := w.WritePacket(columnDefinition(definition)); err != nil {
			return err
		}
	}
	return w.WriteEOF()
}

func writeRows(w *StreamWriter, result *queryResult, definitions []columnMetadata, mode ResultMode) error {
	if mode == ResultModeBinary {
		return writeBinaryResultRows(w, result, definitions)
	}
	return writeTextResultRows(w, result, definitions)
}

func writeTextResultRows(w *StreamWriter, result *queryResult, definitions []columnMetadata) error {
	if result.stream == nil {
		return writeTextRows(w, result.rows, result.nulls, definitions)
	}
	return result.stream(func(row []string, nulls []bool) error {
		payload, err := textRowWithDefinitions(row, 0, [][]bool{nulls}, definitions)
		if err != nil {
			return err
		}
		return w.WritePacket(payload)
	})
}

func writeBinaryResultRows(w *StreamWriter, result *queryResult, definitions []columnMetadata) error {
	if result.stream == nil {
		return writeBinaryRows(w, result.rows, result.nulls, definitions)
	}
	return result.stream(func(row []string, nulls []bool) error {
		payload, err := binaryRow(row, 0, [][]bool{nulls}, definitions)
		if err != nil {
			return err
		}
		return w.WritePacket(payload)
	})
}

func writeTextRows(w *StreamWriter, rows [][]string, nulls [][]bool, definitions []columnMetadata) error {
	for rowIndex, row := range rows {
		payload, err := textRowWithDefinitions(row, rowIndex, nulls, definitions)
		if err != nil {
			return err
		}
		if err := w.WritePacket(payload); err != nil {
			return err
		}
	}
	return nil
}

func writeBinaryRows(w *StreamWriter, rows [][]string, nulls [][]bool, definitions []columnMetadata) error {
	for rowIndex, row := range rows {
		payload, err := binaryRow(row, rowIndex, nulls, definitions)
		if err != nil {
			return err
		}
		if err := w.WritePacket(payload); err != nil {
			return err
		}
	}
	return nil
}

func writeStreamError(w *StreamWriter, err error) error {
	var failure sqlFailure
	if !errors.As(err, &failure) {
		return err
	}
	return w.WritePacket(mysqlError(err))
}

func validateResultSet(result *queryResult, definitions []columnMetadata, mode ResultMode) error {
	if result.stream != nil {
		return nil
	}
	for rowIndex, row := range result.rows {
		for columnIndex, value := range row {
			if resultValueIsNull(rowIndex, columnIndex, result.nulls) {
				continue
			}
			definition := resultColumnDefinition("", columnIndex, definitions)
			if err := validateCell(value, definition, mode); err != nil {
				return sqlFailure{
					code:    1105,
					state:   "HY000",
					message: fmt.Sprintf("wire encoding mismatch for column %s: %v", definition.name, err),
				}
			}
		}
	}
	return nil
}

func validateCell(value string, definition columnMetadata, mode ResultMode) error {
	if mode == ResultModeBinary {
		return validateBinaryValue(value, definition)
	}
	return validateTextValue(value, definition)
}

func validateTextValue(value string, definition columnMetadata) error {
	if definition.typ != mysqlTypeBit {
		return nil
	}
	_, err := encodeBitResultValue(value, definition.length)
	return err
}

func validateBinaryValue(value string, definition columnMetadata) error {
	switch definition.typ {
	case mysqlTypeTiny, mysqlTypeShort, mysqlTypeInt24:
		return validateBinaryInteger(value, binaryIntegerBitSize(definition.typ), definition.flags&mysqlUnsignedFlag != 0)
	case mysqlTypeLong:
		_, err := strconv.ParseInt(value, 10, 32)
		return err
	case mysqlTypeLongLong:
		return validateBinaryLongLong(value, definition.flags&mysqlUnsignedFlag != 0)
	case mysqlTypeFloat:
		_, err := strconv.ParseFloat(value, 32)
		return err
	case mysqlTypeDouble:
		_, err := strconv.ParseFloat(value, 64)
		return err
	case mysqlTypeBit:
		_, err := encodeBitResultValue(value, definition.length)
		return err
	case mysqlTypeDate, mysqlTypeDatetime, mysqlTypeTimestamp, mysqlTypeTime:
		_, err := encodeBinaryTemporal(value, definition.typ)
		return err
	case mysqlTypeYear:
		_, err := encodeBinaryYear(value)
		return err
	default:
		return nil
	}
}

func validateBinaryInteger(value string, bitSize int, unsigned bool) error {
	if unsigned {
		_, err := strconv.ParseUint(value, 10, bitSize)
		return err
	}
	_, err := strconv.ParseInt(value, 10, bitSize)
	return err
}

func validateBinaryLongLong(value string, unsigned bool) error {
	if unsigned {
		_, err := strconv.ParseUint(value, 10, 64)
		return err
	}
	_, err := strconv.ParseInt(value, 10, 64)
	return err
}

func preparedMetadataFits(response []byte, parameters int, metadata []columnMetadata, maximum int64) bool {
	if maximum > 0 && int64(len(response)) > maximum {
		return false
	}
	for index := 0; index < parameters; index++ {
		if maximum > 0 && int64(len(columnDefinition(preparedParameterMetadata(index)))) > maximum {
			return false
		}
	}
	return everyColumnFits(metadata, maximum)
}

func everyColumnFits(metadata []columnMetadata, maximum int64) bool {
	for _, definition := range metadata {
		if maximum > 0 && int64(len(columnDefinition(definition))) > maximum {
			return false
		}
	}
	return true
}

func readPacket(r io.Reader, maximum int64) (byte, []byte, error) {
	if maximum <= 0 {
		return 0, nil, errors.New("packet maximum must be positive")
	}
	var payload []byte
	var sequence, expected byte
	for frame := 0; ; frame++ {
		header := make([]byte, 4)
		if _, err := io.ReadFull(r, header); err != nil {
			return 0, nil, err
		}
		if frame == 0 {
			sequence, expected = header[3], header[3]+1
		} else if header[3] != expected {
			return 0, nil, errors.New("packet continuation sequence mismatch")
		}
		sequence = header[3]
		expected++
		length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
		if int64(len(payload))+int64(length) > maximum {
			return 0, nil, errors.New("packet exceeds configured maximum size")
		}
		start := len(payload)
		payload = append(payload, make([]byte, length)...)
		if _, err := io.ReadFull(r, payload[start:]); err != nil {
			return 0, nil, err
		}
		if length < maximumPacketFrame {
			return sequence, payload, nil
		}
	}
}

func writePacket(w io.Writer, sequence byte, payload []byte) error {
	return NewStreamWriter(w, sequence, 0).WritePacket(payload)
}

func okPacket(affected ...uint64) []byte {
	count := uint64(0)
	if len(affected) > 0 {
		count = affected[0]
	}
	return okPacketWithWarnings(count, 0)
}

func okPacketWithWarnings(affected uint64, warnings uint16) []byte {
	payload := []byte{0x00}
	payload = append(payload, lengthEncodedUint(affected)...)
	payload = append(payload, 0x00, 0x02, 0x00, byte(warnings), byte(warnings>>8))
	return payload
}

func errorPacket(code uint16, state, message string) []byte {
	if len(message) > 255 {
		message = message[:252] + "..."
	}
	payload := []byte{0xff, byte(code), byte(code >> 8), '#'}
	payload = append(payload, state...)
	payload = append(payload, message...)
	return payload
}

func eofPacket(warnings ...uint16) []byte {
	count := uint16(0)
	if len(warnings) > 0 {
		count = warnings[0]
	}
	return []byte{0xfe, byte(count), byte(count >> 8), 2, 0}
}

func handshake(version string, nonce []byte, tlsEnabled bool, connectionID uint32) []byte {
	capabilities := serverCapabilities(tlsEnabled)
	p := []byte{0x0a}
	p = append(p, []byte("8.4.11-database-"+version)...)
	p = append(p, 0)
	p = append(p, byte(connectionID), byte(connectionID>>8), byte(connectionID>>16), byte(connectionID>>24))
	p = append(p, nonce[:8]...)
	p = append(p, 0)
	p = append(p, byte(capabilities), byte(capabilities>>8), 33, 0x02, 0, byte(capabilities>>16), byte(capabilities>>24), byte(len(nonce)+1))
	p = append(p, make([]byte, 10)...)
	p = append(p, nonce[8:]...)
	p = append(p, 0)
	p = append(p, []byte("caching_sha2_password")...)
	p = append(p, 0)
	return p
}

func mysqlError(err error) []byte {
	var failure sqlFailure
	if errors.As(err, &failure) {
		return errorPacket(failure.code, failure.state, failure.message)
	}
	return errorPacket(1064, "42000", err.Error())
}
