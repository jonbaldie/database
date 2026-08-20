package storage

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
)

func (e *Engine) replayWAL() error {
	if e.closed {
		return errClosed
	}
	file, err := os.Open(rowsWalPath(e.directory))
	if errorsIsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		record, done, err := readWALRecord(file)
		if done {
			return nil
		}
		if err != nil {
			return err
		}
		if err := e.applyWALPayload(record); err != nil {
			return err
		}
	}
}

func readWALRecord(file *os.File) ([]byte, bool, error) {
	var length uint32
	if err := binary.Read(file, binary.LittleEndian, &length); err != nil {
		if err == io.EOF {
			return nil, true, nil
		}
		return nil, false, err
	}
	var checksum uint32
	if err := binary.Read(file, binary.LittleEndian, &checksum); err != nil {
		return nil, false, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(file, payload); err != nil {
		return nil, false, err
	}
	if crc32.ChecksumIEEE(payload) != checksum {
		return nil, false, io.ErrUnexpectedEOF
	}
	return payload, false, nil
}

func (e *Engine) applyWALPayload(payload []byte) error {
	kind, namespace, name, row, err := decodePayload(payload)
	if err != nil {
		return err
	}
	current, ok := e.tables[tableKey(namespace, name)]
	if !ok {
		return errMissingTable
	}
	switch kind {
	case walInsert:
		return current.appendRow(row)
	case walUpdate:
		return applyWALUpdate(current, row)
	case walClear:
		clearTable(current)
		return nil
	case walDelete:
		if len(row) == 0 {
			return io.ErrUnexpectedEOF
		}
		return current.deletePrimary(row[0])
	default:
		return io.ErrUnexpectedEOF
	}
}

func applyWALUpdate(current *table, row []string) error {
	if len(row) == len(current.columns)+1 {
		previousPrimary := row[0]
		nextRow := row[1:]
		position, ok := current.primaryIdx[previousPrimary]
		if !ok {
			return errMissingRow
		}
		return current.replaceRow(position, nextRow)
	}
	if err := current.validateRow(row); err != nil {
		return err
	}
	position, ok := current.primaryIdx[current.primaryKey(row)]
	if !ok {
		return current.appendRow(row)
	}
	return current.replaceRow(position, row)
}
