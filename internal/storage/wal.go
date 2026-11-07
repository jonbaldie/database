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

type walOperation struct {
	kind      byte
	namespace string
	table     string
	row       []string
}

type pendingWALTransaction struct {
	operations []walOperation
}

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
	return e.replayWALFile(file)
}

func (e *Engine) replayWALFile(file *os.File) error {
	generation, headerSize, err := inspectWALHeader(file)
	if err != nil {
		return err
	}
	e.walGeneration = generation
	e.walHeaderSize = headerSize
	start, err := e.walReplayStart(generation, headerSize)
	if err != nil {
		return err
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
	return e.replayWALRecords(file)
}

func (e *Engine) walReplayStart(generation uint64, headerSize int64) (int64, error) {
	if !e.checkpoint.loaded {
		return headerSize, nil
	}
	switch generation {
	case e.checkpoint.walGeneration:
		return e.checkpoint.walOffset, nil
	case e.checkpoint.walGeneration + 1:
		if headerSize == 0 {
			return 0, fmt.Errorf("checkpoint WAL generation has no header")
		}
		return headerSize, nil
	default:
		return 0, fmt.Errorf("WAL generation %d does not follow checkpoint generation %d", generation, e.checkpoint.walGeneration)
	}
}

func (e *Engine) replayWALRecords(file *os.File) error {
	var pending *pendingWALTransaction
	for {
		record, done, err := readWALRecord(file)
		if done {
			return nil
		}
		if err != nil {
			return err
		}
		kind, namespace, name, row, err := decodePayload(record)
		if err != nil {
			return err
		}
		switch kind {
		case walBegin:
			pending = &pendingWALTransaction{}
		case walCommit:
			err = pending.commit(e)
			pending = nil
		case walInsert, walUpdate, walClear, walDelete:
			operation := walOperation{kind: kind, namespace: namespace, table: name, row: row}
			err = pending.replayOperation(e, operation)
		default:
			return fmt.Errorf("unknown WAL record kind %d", kind)
		}
		if err != nil {
			return err
		}
	}
}

func (pending *pendingWALTransaction) commit(engine *Engine) error {
	if pending == nil {
		return fmt.Errorf("WAL commit has no pending transaction")
	}
	for _, operation := range pending.operations {
		if err := engine.applyWALOperation(operation); err != nil {
			return err
		}
	}
	return nil
}

func (pending *pendingWALTransaction) replayOperation(engine *Engine, operation walOperation) error {
	if pending == nil {
		return engine.applyWALOperation(operation)
	}
	pending.operations = append(pending.operations, operation)
	return nil
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

func (e *Engine) applyWALOperation(operation walOperation) error {
	current, ok := e.tables[tableKey(operation.namespace, operation.table)]
	if !ok {
		return errMissingTable
	}
	switch operation.kind {
	case walInsert:
		return current.appendRow(operation.row)
	case walUpdate:
		return applyWALUpdate(current, operation.row)
	case walClear:
		clearTable(current)
		return nil
	case walDelete:
		if len(operation.row) == 0 {
			return io.ErrUnexpectedEOF
		}
		return current.deletePrimary(operation.row[0])
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
