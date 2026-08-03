package storage

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"strings"
)

func (t *table) appendRow(row []string) error {
	key := t.primaryKey(row)
	if _, exists := t.primaryIdx[key]; exists {
		return errDuplicateKey
	}
	for _, unique := range t.uniques {
		uniqueKey := rowKey(row, t.columnIndexes(unique))
		indexKey := strings.Join(unique, "\x00")
		if _, exists := t.uniqueIdx[indexKey][uniqueKey]; exists {
			return errDuplicateKey
		}
	}
	position := len(t.rows)
	t.rows = append(t.rows, append([]string(nil), row...))
	t.primaryIdx[key] = position
	for _, unique := range t.uniques {
		indexKey := strings.Join(unique, "\x00")
		t.uniqueIdx[indexKey][rowKey(row, t.columnIndexes(unique))] = position
	}
	return nil
}

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
		delete(t.uniqueIdx[indexKey], rowKey(previous, t.columnIndexes(unique)))
	}
	last := len(t.rows) - 1
	if position != last {
		moved := t.rows[last]
		t.rows[position] = moved
		t.primaryIdx[t.primaryKey(moved)] = position
		for _, unique := range t.uniques {
			indexKey := strings.Join(unique, "\x00")
			t.uniqueIdx[indexKey][rowKey(moved, t.columnIndexes(unique))] = position
		}
	}
	t.rows[last] = nil
	t.rows = t.rows[:last]
	return nil
}

func (t *table) replaceRow(position int, row []string) error {
	previous := t.rows[position]
	previousPrimary := t.primaryKey(previous)
	nextPrimary := t.primaryKey(row)
	if previousPrimary != nextPrimary {
		if _, exists := t.primaryIdx[nextPrimary]; exists {
			return errDuplicateKey
		}
		delete(t.primaryIdx, previousPrimary)
		t.primaryIdx[nextPrimary] = position
	}
	for _, unique := range t.uniques {
		indexKey := strings.Join(unique, "\x00")
		previousUnique := rowKey(previous, t.columnIndexes(unique))
		nextUnique := rowKey(row, t.columnIndexes(unique))
		if previousUnique != nextUnique {
			if _, exists := t.uniqueIdx[indexKey][nextUnique]; exists {
				return errDuplicateKey
			}
			delete(t.uniqueIdx[indexKey], previousUnique)
			t.uniqueIdx[indexKey][nextUnique] = position
		}
	}
	t.rows[position] = append([]string(nil), row...)
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
	parts := make([]string, len(indexes))
	for number, index := range indexes {
		parts[number] = row[index]
	}
	return strings.Join(parts, "\x00")
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
	var builder strings.Builder
	builder.WriteByte(kind)
	builder.WriteString(namespace)
	builder.WriteByte('\x00')
	builder.WriteString(table)
	builder.WriteByte('\x00')
	for index, value := range row {
		if index > 0 {
			builder.WriteByte('\x01')
		}
		builder.WriteString(value)
	}
	return []byte(builder.String())
}

func decodePayload(payload []byte) (byte, string, string, []string, error) {
	if len(payload) < 1 {
		return 0, "", "", nil, io.ErrUnexpectedEOF
	}
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
