// Package storage owns durable row images and point-lookup indexes for one
// data directory. Schema metadata remains in the catalog package.
package storage

import (
	"os"
	"path/filepath"
	"sync"
)

// Engine is the durable row store for one initialized data directory.
type Engine struct {
	mu        sync.RWMutex
	directory string
	tables    map[string]*table
	wal       *os.File
	closed    bool
}

type table struct {
	namespace  string
	name       string
	columns    []string
	primary    []string
	uniques    [][]string
	rows       [][]string
	primaryIdx map[string]int
	uniqueIdx  map[string]map[string]int
}

var (
	errClosed       = errString("storage engine is closed")
	errDuplicateKey = errString("duplicate key")
	errMissingTable = errString("table does not exist")
	errMissingRow   = errString("row does not exist")
	errFinishedTxn  = errString("transaction is finished")
)

type errString string

func (e errString) Error() string { return string(e) }

const (
	walInsert byte = 1
	walUpdate byte = 2
	walClear  byte = 3
	walDelete byte = 4
)

// Open loads durable row state from directory, replaying the WAL when present.
func Open(directory string) (*Engine, error) {
	if err := os.MkdirAll(filepath.Join(directory, "rows"), 0o755); err != nil {
		return nil, err
	}
	engine := &Engine{directory: directory, tables: map[string]*table{}}
	if err := engine.loadMeta(); err != nil {
		return nil, err
	}
	if err := engine.loadCheckpoints(); err != nil {
		return nil, err
	}
	if err := engine.replayWAL(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(rowsWalPath(engine.directory), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, ioSeekEnd); err != nil {
		_ = file.Close()
		return nil, err
	}
	engine.wal = file
	return engine, nil
}

// Close releases the WAL file handle.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if e.wal == nil {
		return nil
	}
	err := e.wal.Close()
	e.wal = nil
	return err
}

func tableKey(namespace, name string) string {
	return namespace + "\x00" + name
}

func rowsWalPath(directory string) string {
	return filepath.Join(directory, "rows", "wal.log")
}

func rowsMetaPath(directory string) string {
	return filepath.Join(directory, "rows", "tables.meta")
}

func rowsCheckpointPath(directory, namespace, name string) string {
	return filepath.Join(directory, "rows", namespace+"."+name+".chk")
}

const ioSeekEnd = 2
