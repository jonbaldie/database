package storage

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"strings"
)

func (t *table) appendRow(row []string) error {
	if err := t.validateRow(row); err != nil {
		return err
	}
	key, err := t.newPrimaryKey(row)
	if err != nil {
		return err
	}
	if err := t.validateUniqueKeys(row); err != nil {
		return err
	}
	position := len(t.rows)
	t.rows = append(t.rows, append([]string(nil), row...))
	t.addPrimaryIndex(key, position)
	t.addUniqueIndexes(row, position)
	return nil
}

func (t *table) newPrimaryKey(row []string) (string, error) {
	if len(t.primary) == 0 {
		return "", nil
	}
	key := t.primaryKey(row)
	if _, exists := t.primaryIdx[key]; exists {
		return "", errDuplicateKey
	}
	return key, nil
}

func (t *table) validateUniqueKeys(row []string) error {
	for _, unique := range t.uniques {
		indexes := t.columnIndexes(unique)
		if uniqueKeyNullable(row, indexes) {
			continue
		}
		indexKey := strings.Join(unique, "\x00")
		if _, exists := t.uniqueIdx[indexKey][rowKey(row, indexes)]; exists {
			return errDuplicateKey
		}
	}
	return nil
}

func (t *table) addPrimaryIndex(key string, position int) {
	if len(t.primary) > 0 {
		t.primaryIdx[key] = position
	}
}

func (t *table) addUniqueIndexes(row []string, position int) {
	for _, unique := range t.uniques {
		indexes := t.columnIndexes(unique)
		if uniqueKeyNullable(row, indexes) {
			continue
		}
		indexKey := strings.Join(unique, "\x00")
		t.uniqueIdx[indexKey][rowKey(row, indexes)] = position
	}
}

func uniqueKeyNullable(row []string, indexes []int) bool {
	for _, index := range indexes {
		if index < 0 || index >= len(row) || row[index] == storedNullValue {
			return true
		}
	}
	return false
}

const storedNullValue = "\x00database-sql-null"

func clearTable(current *table) {
	current.rows = nil
	current.primaryIdx = map[string]int{}
	for _, unique := range current.uniques {
		current.uniqueIdx[strings.Join(unique, "\x00")] = map[string]int{}
	}
}

func (t *table) deletePrimary(primary string) error {
	position, ok := t.primaryIdx[primary]
	if !ok {
		return errMissingRow
	}
	previous := t.rows[position]
	delete(t.primaryIdx, primary)
	for _, unique := range t.uniques {
		indexKey := strings.Join(unique, "\x00")
		indexes := t.columnIndexes(unique)
		if !uniqueKeyNullable(previous, indexes) {
			delete(t.uniqueIdx[indexKey], rowKey(previous, indexes))
		}
	}
	last := len(t.rows) - 1
	if position != last {
		moved := t.rows[last]
		t.rows[position] = moved
		t.primaryIdx[t.primaryKey(moved)] = position
		for _, unique := range t.uniques {
			indexKey := strings.Join(unique, "\x00")
			indexes := t.columnIndexes(unique)
			if !uniqueKeyNullable(moved, indexes) {
				t.uniqueIdx[indexKey][rowKey(moved, indexes)] = position
			}
		}
	}
	t.rows[last] = nil
	t.rows = t.rows[:last]
	return nil
}

func (t *table) replaceRow(position int, row []string) error {
	if position < 0 || position >= len(t.rows) {
		return errMissingRow
	}
	if err := t.validateRow(row); err != nil {
		return err
	}
	previous := t.rows[position]
	if err := t.validatePrimaryReplacement(previous, row, position); err != nil {
		return err
	}
	if err := t.validateUniqueReplacement(previous, row, position); err != nil {
		return err
	}
	previousPrimary := t.primaryKey(previous)
	nextPrimary := t.primaryKey(row)
	if previousPrimary != nextPrimary {
		delete(t.primaryIdx, previousPrimary)
		t.primaryIdx[nextPrimary] = position
	}
	t.replaceUniqueIndexes(previous, row, position)
	t.rows[position] = append([]string(nil), row...)
	return nil
}

func (t *table) validatePrimaryReplacement(previous, row []string, position int) error {
	previousKey := t.primaryKey(previous)
	nextKey := t.primaryKey(row)
	if previousKey == nextKey {
		return nil
	}
	if existing, exists := t.primaryIdx[nextKey]; exists && existing != position {
		return errDuplicateKey
	}
	return nil
}

func (t *table) validateUniqueReplacement(previous, row []string, position int) error {
	for _, unique := range t.uniques {
		if err := t.validateUniqueIndexReplacement(previous, row, position, unique); err != nil {
			return err
		}
	}
	return nil
}

func (t *table) validateUniqueIndexReplacement(previous, row []string, position int, unique []string) error {
	indexes := t.columnIndexes(unique)
	if uniqueKeyNullable(previous, indexes) || uniqueKeyNullable(row, indexes) {
		return nil
	}
	previousKey := rowKey(previous, indexes)
	nextKey := rowKey(row, indexes)
	if previousKey == nextKey {
		return nil
	}
	indexKey := strings.Join(unique, "\x00")
	if existing, exists := t.uniqueIdx[indexKey][nextKey]; exists && existing != position {
		return errDuplicateKey
	}
	return nil
}

func (t *table) replaceUniqueIndexes(previous, row []string, position int) {
	for _, unique := range t.uniques {
		t.replaceUniqueIndex(previous, row, position, unique)
	}
}

func (t *table) replaceUniqueIndex(previous, row []string, position int, unique []string) {
	indexes := t.columnIndexes(unique)
	previousNullable := uniqueKeyNullable(previous, indexes)
	nextNullable := uniqueKeyNullable(row, indexes)
	previousKey := rowKey(previous, indexes)
	nextKey := rowKey(row, indexes)
	indexKey := strings.Join(unique, "\x00")
	switch {
	case previousNullable && nextNullable:
	case previousNullable:
		t.uniqueIdx[indexKey][nextKey] = position
	case nextNullable:
		delete(t.uniqueIdx[indexKey], previousKey)
	case previousKey != nextKey:
		delete(t.uniqueIdx[indexKey], previousKey)
		t.uniqueIdx[indexKey][nextKey] = position
	}
}

func (t *table) validateRow(row []string) error {
	if len(row) != len(t.columns) {
		return errInvalidRow
	}
	return nil
}

func (t *table) primaryKey(row []string) string {
	return rowKey(row, t.columnIndexes(t.primary))
}

func (t *table) columnIndexes(names []string) []int {
	indexes := make([]int, len(names))
	for number, name := range names {
		for index, column := range t.columns {
			if column == name {
				indexes[number] = index
				break
			}
		}
	}
	return indexes
}

func rowKey(row []string, indexes []int) string {
	if len(indexes) == 1 {
		return row[indexes[0]]
	}
	parts := make([]string, len(indexes))
	for number, index := range indexes {
		value := row[index]
		parts[number] = strconv.Itoa(len(value)) + ":" + value
	}
	return strings.Join(parts, "")
}

func cloneTable(source *table) *table {
	cloned := &table{
		namespace: source.namespace,
		name:      source.name,
		columns:   append([]string(nil), source.columns...),
		primary:   append([]string(nil), source.primary...),
		uniques:   cloneStringMatrix(source.uniques),
		rows:      cloneRows(source.rows),
		uniqueIdx: map[string]map[string]int{},
	}
	rebuildIndexes(cloned)
	return cloned
}

func cloneRows(rows [][]string) [][]string {
	cloned := make([][]string, len(rows))
	for index, row := range rows {
		cloned[index] = append([]string(nil), row...)
	}
	return cloned
}

func writeRow(writer io.Writer, row []string) error {
	if err := binary.Write(writer, binary.LittleEndian, uint32(len(row))); err != nil {
		return err
	}
	for _, value := range row {
		if err := binary.Write(writer, binary.LittleEndian, uint32(len(value))); err != nil {
			return err
		}
		if _, err := writer.Write([]byte(value)); err != nil {
			return err
		}
	}
	return nil
}

func readRow(reader io.Reader) ([]string, error) {
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return nil, err
	}
	row := make([]string, count)
	for index := range row {
		var length uint32
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
			return nil, err
		}
		buffer := make([]byte, length)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return nil, err
		}
		row[index] = string(buffer)
	}
	return row, nil
}

func writeWALRecord(file *os.File, kind byte, namespace, table string, row []string) error {
	payload := encodePayload(kind, namespace, table, row)
	checksum := crc32.ChecksumIEEE(payload)
	if err := binary.Write(file, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, checksum); err != nil {
		return err
	}
	_, err := file.Write(payload)
	return err
}

func encodePayload(kind byte, namespace, table string, row []string) []byte {
	payload := []byte{walPayloadMagic, kind}
	payload = appendWALField(payload, namespace)
	payload = appendWALField(payload, table)
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], uint32(len(row)))
	payload = append(payload, count[:]...)
	for _, value := range row {
		payload = appendWALField(payload, value)
	}
	return payload
}

func decodePayload(payload []byte) (byte, string, string, []string, error) {
	if len(payload) < 1 {
		return 0, "", "", nil, io.ErrUnexpectedEOF
	}
	if payload[0] == walPayloadMagic {
		return decodeVersionedPayload(payload)
	}
	return decodeLegacyPayload(payload)
}

const walPayloadMagic byte = 0xd2

func appendWALField(payload []byte, value string) []byte {
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	payload = append(payload, length[:]...)
	return append(payload, value...)
}

func decodeVersionedPayload(payload []byte) (byte, string, string, []string, error) {
	if len(payload) < 2 {
		return 0, "", "", nil, io.ErrUnexpectedEOF
	}
	offset := 1
	kind := payload[offset]
	offset++
	namespace, err := readWALField(payload, &offset)
	if err != nil {
		return 0, "", "", nil, err
	}
	table, err := readWALField(payload, &offset)
	if err != nil {
		return 0, "", "", nil, err
	}
	if len(payload)-offset < 4 {
		return 0, "", "", nil, io.ErrUnexpectedEOF
	}
	count := binary.LittleEndian.Uint32(payload[offset : offset+4])
	offset += 4
	if count > uint32((len(payload)-offset)/4) {
		return 0, "", "", nil, io.ErrUnexpectedEOF
	}
	row := make([]string, 0, int(count))
	for index := uint32(0); index < count; index++ {
		value, err := readWALField(payload, &offset)
		if err != nil {
			return 0, "", "", nil, err
		}
		row = append(row, value)
	}
	if offset != len(payload) {
		return 0, "", "", nil, io.ErrUnexpectedEOF
	}
	return kind, namespace, table, row, nil
}

func readWALField(payload []byte, offset *int) (string, error) {
	if len(payload)-*offset < 4 {
		return "", io.ErrUnexpectedEOF
	}
	length := binary.LittleEndian.Uint32(payload[*offset : *offset+4])
	*offset += 4
	if uint64(length) > uint64(len(payload)-*offset) {
		return "", io.ErrUnexpectedEOF
	}
	value := string(payload[*offset : *offset+int(length)])
	*offset += int(length)
	return value, nil
}

func decodeLegacyPayload(payload []byte) (byte, string, string, []string, error) {
	kind := payload[0]
	rest := string(payload[1:])
	parts := strings.SplitN(rest, "\x00", 3)
	if len(parts) != 3 {
		return 0, "", "", nil, io.ErrUnexpectedEOF
	}
	row := []string{}
	if parts[2] != "" {
		row = strings.Split(parts[2], "\x01")
	}
	return kind, parts[0], parts[1], row, nil
}
