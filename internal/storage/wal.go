package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

var walFileMagic = []byte("DBWAL001")

const walHeaderSize = int64(16)

func (e *Engine) replayWAL() error {
	if e.closed {
		return errClosed
	}
	file, err := os.Open(rowsWalPath(e.directory))
	if errorsIsNotExist(err) {
		if e.checkpoint.loaded {
			return fmt.Errorf("WAL is missing after checkpoint")
		}
		e.walGeneration = 0
		e.walHeaderSize = 0
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	generation, headerSize, err := inspectWALHeader(file)
	if err != nil {
		return err
	}
	e.walGeneration = generation
	e.walHeaderSize = headerSize
	start := headerSize
	if e.checkpoint.loaded {
		switch generation {
		case e.checkpoint.walGeneration:
			start = e.checkpoint.walOffset
		case e.checkpoint.walGeneration + 1:
			if headerSize == 0 {
				return fmt.Errorf("checkpoint WAL generation has no header")
			}
			start = headerSize
		default:
			return fmt.Errorf("WAL generation %d does not follow checkpoint generation %d", generation, e.checkpoint.walGeneration)
		}
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if start < headerSize || start > info.Size() {
		return fmt.Errorf("checkpoint WAL offset %d is outside WAL size %d", start, info.Size())
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
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

func inspectWALHeader(file *os.File) (uint64, int64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, err
	}
	magic := make([]byte, len(walFileMagic))
	if _, err := io.ReadFull(file, magic); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
				return 0, 0, seekErr
			}
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if !bytes.Equal(magic, walFileMagic) {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return 0, 0, err
		}
		return 0, 0, nil
	}
	var generation uint64
	if err := binary.Read(file, binary.LittleEndian, &generation); err != nil {
		return 0, 0, err
	}
	return generation, walHeaderSize, nil
}

func writeWALHeader(file *os.File, generation uint64) error {
	if _, err := file.Write(walFileMagic); err != nil {
		return err
	}
	return binary.Write(file, binary.LittleEndian, generation)
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
