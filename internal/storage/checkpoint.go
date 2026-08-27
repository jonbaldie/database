package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
)

var checkpointFileMagic = []byte("DBCHK001")

const (
	checkpointEveryCommits = 64
	checkpointWALBytes     = 1 << 20
	checkpointDigestSize   = sha256.Size
)

type checkpointState struct {
	loaded        bool
	walGeneration uint64
	walOffset     int64
}

type checkpointPhase string

const (
	checkpointTempSynced checkpointPhase = "checkpoint temporary file synced"
	checkpointPublished  checkpointPhase = "checkpoint published"
	checkpointWALSynced  checkpointPhase = "replacement WAL synced"
	checkpointWALRotated checkpointPhase = "replacement WAL published"
)

func (e *Engine) maybeCheckpointLocked() error {
	position, err := e.wal.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if e.commitsSinceCheckpoint < checkpointEveryCommits && position-e.walHeaderSize < checkpointWALBytes {
		return nil
	}
	return e.checkpointLocked(position)
}

func (e *Engine) checkpointLocked(walOffset int64) error {
	if err := e.publishCheckpointSnapshot(walOffset); err != nil {
		return err
	}
	nextGeneration := e.walGeneration + 1
	nextWAL, err := e.prepareReplacementWAL(nextGeneration)
	if err != nil {
		return err
	}
	return e.publishReplacementWAL(nextWAL, nextGeneration, walOffset)
}

func (e *Engine) publishCheckpointSnapshot(walOffset int64) error {
	checkpointTemporary := rowsGlobalCheckpointPath(e.directory) + ".tmp"
	if err := e.writeCheckpointTemporary(checkpointTemporary, walOffset); err != nil {
		return err
	}
	if err := e.callCheckpointHook(checkpointTempSynced); err != nil {
		return err
	}
	if err := os.Rename(checkpointTemporary, rowsGlobalCheckpointPath(e.directory)); err != nil {
		return err
	}
	if err := e.syncCheckpointDirectory(); err != nil {
		return err
	}
	return e.callCheckpointHook(checkpointPublished)
}

func (e *Engine) prepareReplacementWAL(nextGeneration uint64) (*os.File, error) {
	walTemporary := rowsWalPath(e.directory) + ".next"
	nextWAL, err := openReplacementWAL(walTemporary, nextGeneration)
	if err != nil {
		return nil, err
	}
	if err := e.callCheckpointHook(checkpointWALSynced); err != nil {
		_ = nextWAL.Close()
		return nil, err
	}
	return nextWAL, nil
}

func (e *Engine) publishReplacementWAL(nextWAL *os.File, nextGeneration uint64, walOffset int64) error {
	walTemporary := rowsWalPath(e.directory) + ".next"
	if err := os.Rename(walTemporary, rowsWalPath(e.directory)); err != nil {
		_ = nextWAL.Close()
		return err
	}
	if _, err := nextWAL.Seek(0, io.SeekEnd); err != nil {
		_ = nextWAL.Close()
		return err
	}
	if err := e.syncCheckpointDirectory(); err != nil {
		e.stopAfterWALPublicationFailure(nextWAL)
		return err
	}
	previousWAL := e.wal
	e.wal = nextWAL
	e.walGeneration = nextGeneration
	e.walHeaderSize = walHeaderSize
	e.checkpoint = checkpointState{loaded: true, walGeneration: nextGeneration - 1, walOffset: walOffset}
	e.commitsSinceCheckpoint = 0
	closeErr := previousWAL.Close()
	if closeErr != nil {
		return closeErr
	}
	return e.callCheckpointHook(checkpointWALRotated)
}

func (e *Engine) stopAfterWALPublicationFailure(nextWAL *os.File) {
	_ = nextWAL.Close()
	if e.wal != nil {
		_ = e.wal.Close()
		e.wal = nil
	}
	e.closed = true
}

func (e *Engine) writeCheckpointTemporary(path string, walOffset int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	writer := io.MultiWriter(file, hasher)
	writeErr := e.writeCheckpointBody(writer, walOffset)
	if writeErr == nil {
		_, writeErr = file.Write(hasher.Sum(nil))
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (e *Engine) writeCheckpointBody(writer io.Writer, walOffset int64) error {
	if err := e.writeCheckpointHeader(writer, walOffset); err != nil {
		return err
	}
	keys := make([]string, 0, len(e.tables))
	for key := range e.tables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if err := binary.Write(writer, binary.LittleEndian, uint32(len(keys))); err != nil {
		return err
	}
	for _, key := range keys {
		if err := writeCheckpointTable(writer, e.tables[key]); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) writeCheckpointHeader(writer io.Writer, walOffset int64) error {
	if _, err := writer.Write(checkpointFileMagic); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, e.walGeneration); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, walOffset); err != nil {
		return err
	}
	return nil
}

func writeCheckpointTable(writer io.Writer, current *table) error {
	if err := writeCheckpointString(writer, current.namespace); err != nil {
		return err
	}
	if err := writeCheckpointString(writer, current.name); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, uint64(len(current.rows))); err != nil {
		return err
	}
	for _, row := range current.rows {
		if err := writeRow(writer, row); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) loadGlobalCheckpoint() (bool, error) {
	file, err := os.Open(rowsGlobalCheckpointPath(e.directory))
	if errorsIsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	state, err := e.decodeGlobalCheckpoint(file)
	if err != nil {
		return false, err
	}
	e.checkpoint = state
	return true, nil
}

func (e *Engine) decodeGlobalCheckpoint(file *os.File) (checkpointState, error) {
	info, err := file.Stat()
	if err != nil {
		return checkpointState{}, err
	}
	if info.Size() < int64(len(checkpointFileMagic)+checkpointDigestSize) {
		return checkpointState{}, fmt.Errorf("checkpoint is incomplete")
	}
	bodySize := info.Size() - checkpointDigestSize
	limited := &io.LimitedReader{R: file, N: bodySize}
	hasher := sha256.New()
	reader := io.TeeReader(limited, hasher)
	state, err := e.readCheckpointBody(reader, uint64(bodySize))
	if err != nil {
		return checkpointState{}, err
	}
	if limited.N != 0 {
		return checkpointState{}, fmt.Errorf("checkpoint contains trailing data")
	}
	if err := verifyCheckpointDigest(file, hasher.Sum(nil)); err != nil {
		return checkpointState{}, err
	}
	return state, nil
}

func verifyCheckpointDigest(file *os.File, computed []byte) error {
	storedDigest := make([]byte, checkpointDigestSize)
	if _, err := io.ReadFull(file, storedDigest); err != nil {
		return err
	}
	if !bytes.Equal(storedDigest, computed) {
		return fmt.Errorf("checkpoint checksum does not match")
	}
	return nil
}

func (e *Engine) readCheckpointBody(reader io.Reader, maximum uint64) (checkpointState, error) {
	state, tableCount, err := readCheckpointHeader(reader, maximum)
	if err != nil {
		return checkpointState{}, err
	}
	for range tableCount {
		if err := e.readCheckpointTable(reader, maximum); err != nil {
			return checkpointState{}, err
		}
	}
	return state, nil
}

func readCheckpointHeader(reader io.Reader, maximum uint64) (checkpointState, uint32, error) {
	magic := make([]byte, len(checkpointFileMagic))
	if _, err := io.ReadFull(reader, magic); err != nil {
		return checkpointState{}, 0, err
	}
	if !bytes.Equal(magic, checkpointFileMagic) {
		return checkpointState{}, 0, fmt.Errorf("checkpoint header is invalid")
	}
	state := checkpointState{loaded: true}
	if err := binary.Read(reader, binary.LittleEndian, &state.walGeneration); err != nil {
		return checkpointState{}, 0, err
	}
	if err := binary.Read(reader, binary.LittleEndian, &state.walOffset); err != nil {
		return checkpointState{}, 0, err
	}
	if state.walOffset < 0 {
		return checkpointState{}, 0, fmt.Errorf("checkpoint WAL offset is invalid")
	}
	var tableCount uint32
	if err := binary.Read(reader, binary.LittleEndian, &tableCount); err != nil {
		return checkpointState{}, 0, err
	}
	if uint64(tableCount) > maximum {
		return checkpointState{}, 0, fmt.Errorf("checkpoint table count is invalid")
	}
	return state, tableCount, nil
}

func (e *Engine) readCheckpointTable(reader io.Reader, maximum uint64) error {
	namespace, err := readCheckpointString(reader, maximum)
	if err != nil {
		return err
	}
	name, err := readCheckpointString(reader, maximum)
	if err != nil {
		return err
	}
	current, ok := e.tables[tableKey(namespace, name)]
	if !ok {
		return fmt.Errorf("checkpoint references missing table %s.%s", namespace, name)
	}
	clearTable(current)
	var rowCount uint64
	if err := binary.Read(reader, binary.LittleEndian, &rowCount); err != nil {
		return err
	}
	if rowCount > maximum {
		return fmt.Errorf("checkpoint row count is invalid")
	}
	return readCheckpointRows(reader, current, rowCount)
}

func readCheckpointRows(reader io.Reader, current *table, rowCount uint64) error {
	for range rowCount {
		row, err := readRow(reader)
		if err != nil {
			return err
		}
		row = padCheckpointRow(row, len(current.columns))
		if err := current.appendRow(row); err != nil {
			return err
		}
	}
	return nil
}

func padCheckpointRow(row []string, columnCount int) []string {
	if len(row) >= columnCount {
		return row
	}
	padded := make([]string, columnCount)
	copy(padded, row)
	return padded
}

func writeCheckpointString(writer io.Writer, value string) error {
	if err := binary.Write(writer, binary.LittleEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(writer, value)
	return err
}

func readCheckpointString(reader io.Reader, maximum uint64) (string, error) {
	var length uint32
	if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	if uint64(length) > maximum {
		return "", fmt.Errorf("checkpoint string length is invalid")
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return string(value), nil
}

func openReplacementWAL(path string, generation uint64) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	writeErr := writeWALHeader(file, generation)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if writeErr != nil {
		_ = file.Close()
		return nil, writeErr
	}
	return file, nil
}

func syncRowsDirectory(directory string) error {
	rows, err := os.Open(rowsWalDirectory(directory))
	if err != nil {
		return err
	}
	syncErr := rows.Sync()
	closeErr := rows.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (e *Engine) syncCheckpointDirectory() error {
	if e.checkpointSyncHook != nil {
		return e.checkpointSyncHook()
	}
	return syncRowsDirectory(e.directory)
}

func (e *Engine) callCheckpointHook(phase checkpointPhase) error {
	if e.checkpointHook == nil {
		return nil
	}
	return e.checkpointHook(phase)
}
