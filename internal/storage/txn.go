package storage

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"strings"
)

type tableOverlay struct {
	base    *table
	inserts [][]string
	updates map[string][]string
	deletes map[string]bool
	clear   bool
}

// Begin starts a private mutation transaction.
func (e *Engine) Begin() (*Transaction, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return nil, errClosed
	}
	return &Transaction{engine: e, overlays: map[string]*tableOverlay{}}, nil
}

// Transaction stages row mutations until Commit fsyncs them into the WAL.
type Transaction struct {
	engine   *Engine
	overlays map[string]*tableOverlay
	finished bool
}

// Insert stages one row into the transaction.
func (txn *Transaction) Insert(namespace, name string, row []string) error {
	if txn.finished {
		return errFinishedTxn
	}
	overlay, err := txn.overlay(namespace, name)
	if err != nil {
		return err
	}
	if len(overlay.base.primary) == 0 {
		overlay.inserts = append(overlay.inserts, append([]string(nil), row...))
		return nil
	}
	key := overlay.base.primaryKey(row)
	if !overlay.clear {
		if _, exists := overlay.base.primaryIdx[key]; exists && !overlay.deletes[key] {
			return errDuplicateKey
		}
	}
	for _, staged := range overlay.inserts {
		if overlay.base.primaryKey(staged) == key {
			return errDuplicateKey
		}
	}
	if _, exists := overlay.updates[key]; exists {
		return errDuplicateKey
	}
	if !overlay.clear {
		for _, unique := range overlay.base.uniques {
			indexes := overlay.base.columnIndexes(unique)
			if uniqueKeyNullable(row, indexes) {
				continue
			}
			uniqueKey := rowKey(row, indexes)
			indexKey := strings.Join(unique, "\x00")
			if position, exists := overlay.base.uniqueIdx[indexKey][uniqueKey]; exists {
				existing := overlay.base.primaryKey(overlay.base.rows[position])
				if !overlay.deletes[existing] {
					return errDuplicateKey
				}
			}
			for _, staged := range overlay.inserts {
				if uniqueKeyNullable(staged, indexes) {
					continue
				}
				if rowKey(staged, indexes) == uniqueKey {
					return errDuplicateKey
				}
			}
		}
	} else {
		for _, unique := range overlay.base.uniques {
			indexes := overlay.base.columnIndexes(unique)
			if uniqueKeyNullable(row, indexes) {
				continue
			}
			uniqueKey := rowKey(row, indexes)
			for _, staged := range overlay.inserts {
				if uniqueKeyNullable(staged, indexes) {
					continue
				}
				if rowKey(staged, indexes) == uniqueKey {
					return errDuplicateKey
				}
			}
		}
	}
	if overlay.deletes != nil {
		delete(overlay.deletes, key)
	}
	overlay.inserts = append(overlay.inserts, append([]string(nil), row...))
	return nil
}

// UpdatePrimary replaces the row identified by primary key.
func (txn *Transaction) UpdatePrimary(namespace, name, primary string, row []string) error {
	if txn.finished {
		return errFinishedTxn
	}
	overlay, err := txn.overlay(namespace, name)
	if err != nil {
		return err
	}
	if overlay.clear {
		return errMissingRow
	}
	if _, ok := overlay.base.primaryIdx[primary]; !ok {
		return errMissingRow
	}
	if overlay.deletes[primary] {
		return errMissingRow
	}
	if overlay.updates == nil {
		overlay.updates = map[string][]string{}
	}
	overlay.updates[primary] = append([]string(nil), row...)
	return nil
}

// Clear removes every row from one table.
func (txn *Transaction) Clear(namespace, name string) error {
	if txn.finished {
		return errFinishedTxn
	}
	overlay, err := txn.overlay(namespace, name)
	if err != nil {
		return err
	}
	overlay.clear = true
	overlay.inserts = nil
	overlay.updates = nil
	overlay.deletes = nil
	return nil
}

// DeletePrimary removes the row identified by primary key.
func (txn *Transaction) DeletePrimary(namespace, name, primary string) error {
	if txn.finished {
		return errFinishedTxn
	}
	overlay, err := txn.overlay(namespace, name)
	if err != nil {
		return err
	}
	if overlay.clear {
		return nil
	}
	if _, ok := overlay.base.primaryIdx[primary]; !ok {
		return errMissingRow
	}
	if overlay.deletes == nil {
		overlay.deletes = map[string]bool{}
	}
	delete(overlay.updates, primary)
	overlay.deletes[primary] = true
	filtered := overlay.inserts[:0]
	for _, row := range overlay.inserts {
		if overlay.base.primaryKey(row) == primary {
			continue
		}
		filtered = append(filtered, row)
	}
	overlay.inserts = filtered
	return nil
}

func (txn *Transaction) overlay(namespace, name string) (*tableOverlay, error) {
	key := tableKey(namespace, name)
	if staged, ok := txn.overlays[key]; ok {
		return staged, nil
	}
	txn.engine.mu.RLock()
	base, ok := txn.engine.tables[key]
	txn.engine.mu.RUnlock()
	if !ok {
		return nil, errMissingTable
	}
	staged := &tableOverlay{base: base}
	txn.overlays[key] = staged
	return staged, nil
}

// Commit publishes staged mutations and durable-syncs the WAL.
func (txn *Transaction) Commit() error {
	if txn.finished {
		return errFinishedTxn
	}
	txn.engine.mu.Lock()
	if txn.engine.closed {
		txn.engine.mu.Unlock()
		return errClosed
	}
	for _, overlay := range txn.overlays {
		if err := txn.writeOverlay(overlay); err != nil {
			txn.engine.mu.Unlock()
			return err
		}
	}
	txn.engine.mu.Unlock()
	if err := txn.engine.awaitGroupSync(); err != nil {
		return err
	}
	txn.engine.mu.Lock()
	defer txn.engine.mu.Unlock()
	if txn.engine.closed {
		return errClosed
	}
	for _, overlay := range txn.overlays {
		if err := applyOverlay(overlay); err != nil {
			return err
		}
	}
	txn.finished = true
	return nil
}

func (txn *Transaction) writeOverlay(overlay *tableOverlay) error {
	if overlay.clear {
		if err := writeWALRecord(txn.engine.wal, walClear, overlay.base.namespace, overlay.base.name, nil); err != nil {
			return err
		}
	}
	for primary := range overlay.deletes {
		if err := writeWALRecord(txn.engine.wal, walDelete, overlay.base.namespace, overlay.base.name, []string{primary}); err != nil {
			return err
		}
	}
	for _, row := range overlay.inserts {
		if err := writeWALRecord(txn.engine.wal, walInsert, overlay.base.namespace, overlay.base.name, row); err != nil {
			return err
		}
	}
	for _, row := range overlay.updates {
		if err := writeWALRecord(txn.engine.wal, walUpdate, overlay.base.namespace, overlay.base.name, row); err != nil {
			return err
		}
	}
	return nil
}

func applyOverlay(overlay *tableOverlay) error {
	if overlay.clear {
		clearTable(overlay.base)
	}
	for primary := range overlay.deletes {
		if err := overlay.base.deletePrimary(primary); err != nil {
			return err
		}
	}
	for primary, row := range overlay.updates {
		position := overlay.base.primaryIdx[primary]
		if err := overlay.base.replaceRow(position, row); err != nil {
			return err
		}
	}
	for _, row := range overlay.inserts {
		if err := overlay.base.appendRow(row); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) replayWAL() error {
	file, err := os.Open(e.walPath())
	if errorsIsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		var length uint32
		if err := binary.Read(file, binary.LittleEndian, &length); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var checksum uint32
		if err := binary.Read(file, binary.LittleEndian, &checksum); err != nil {
			return err
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(file, payload); err != nil {
			return err
		}
		if crc32.ChecksumIEEE(payload) != checksum {
			return io.ErrUnexpectedEOF
		}
		if err := e.applyWALPayload(payload); err != nil {
			return err
		}
	}
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
		position, ok := current.primaryIdx[current.primaryKey(row)]
		if !ok {
			return current.appendRow(row)
		}
		return current.replaceRow(position, row)
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
