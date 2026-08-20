package storage

import (
	"io"
	"os"
	"sort"
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
	if err := overlay.base.validateRow(row); err != nil {
		return err
	}
	if err := overlay.rejectDuplicateInsert(row); err != nil {
		return err
	}
	key := overlay.base.primaryKey(row)
	if overlay.deletes != nil {
		delete(overlay.deletes, key)
	}
	overlay.inserts = append(overlay.inserts, append([]string(nil), row...))
	return nil
}

func (overlay *tableOverlay) rejectDuplicateInsert(row []string) error {
	key := overlay.base.primaryKey(row)
	if err := overlay.rejectDuplicatePrimary(key); err != nil {
		return err
	}
	return overlay.rejectDuplicateUniques(row)
}

func (overlay *tableOverlay) rejectDuplicatePrimary(key string) error {
	if len(overlay.base.primary) == 0 {
		return nil
	}
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
	return nil
}

func (overlay *tableOverlay) rejectDuplicateUniques(row []string) error {
	for _, unique := range overlay.base.uniques {
		if err := overlay.rejectDuplicateUnique(row, unique); err != nil {
			return err
		}
	}
	return nil
}

func (overlay *tableOverlay) rejectDuplicateUnique(row, unique []string) error {
	indexes := overlay.base.columnIndexes(unique)
	if uniqueKeyNullable(row, indexes) {
		return nil
	}
	uniqueKey := rowKey(row, indexes)
	if !overlay.clear {
		indexKey := strings.Join(unique, "\x00")
		if position, exists := overlay.base.uniqueIdx[indexKey][uniqueKey]; exists {
			existing := overlay.base.primaryKey(overlay.base.rows[position])
			if !overlay.deletes[existing] {
				return errDuplicateKey
			}
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
	if err := overlay.base.validateRow(row); err != nil {
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

// Commit validates and applies all staged mutations before publishing them and
// durable-syncing the WAL. A rejected overlay must never leave a replayable
// record behind.
func (txn *Transaction) Commit() error {
	if txn.finished {
		return errFinishedTxn
	}

	engine := txn.engine
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := ensureCommitReady(engine); err != nil {
		return err
	}

	keys := changedOverlayKeys(txn.overlays)
	if len(keys) == 0 {
		txn.finished = true
		return nil
	}
	staged, err := stageOverlays(engine, txn.overlays, keys)
	if err != nil {
		return err
	}
	if err := appendTransactionWAL(engine.wal, txn.overlays, keys); err != nil {
		return err
	}
	publishStagedTables(engine, staged)
	txn.finished = true
	return nil
}

func ensureCommitReady(engine *Engine) error {
	if engine.closed || engine.wal == nil {
		return errClosed
	}
	return nil
}

func stageOverlays(engine *Engine, overlays map[string]*tableOverlay, keys []string) (map[string]*table, error) {
	staged := make(map[string]*table, len(keys))
	for _, key := range keys {
		overlay := overlays[key]
		current, ok := engine.tables[key]
		if !ok || current != overlay.base {
			return nil, errMissingTable
		}
		copy := cloneTable(current)
		if err := applyOverlay(copy, overlay); err != nil {
			return nil, err
		}
		staged[key] = copy
	}
	return staged, nil
}

func appendTransactionWAL(file *os.File, overlays map[string]*tableOverlay, keys []string) error {
	start, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := writeOverlay(file, overlays[key]); err != nil {
			_ = file.Truncate(start)
			_, _ = file.Seek(0, io.SeekEnd)
			return err
		}
	}
	return file.Sync()
}

func publishStagedTables(engine *Engine, staged map[string]*table) {
	for key, copy := range staged {
		engine.tables[key] = copy
	}
}

func changedOverlayKeys(overlays map[string]*tableOverlay) []string {
	keys := make([]string, 0, len(overlays))
	for key, overlay := range overlays {
		if overlayChanged(overlay) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func overlayChanged(overlay *tableOverlay) bool {
	return overlay.clear || len(overlay.inserts) > 0 || len(overlay.updates) > 0 || len(overlay.deletes) > 0
}

func writeOverlay(file *os.File, overlay *tableOverlay) error {
	if overlay.clear {
		if err := writeWALRecord(file, walClear, overlay.base.namespace, overlay.base.name, nil); err != nil {
			return err
		}
	}
	for _, primary := range sortedKeys(overlay.deletes) {
		if err := writeWALRecord(file, walDelete, overlay.base.namespace, overlay.base.name, []string{primary}); err != nil {
			return err
		}
	}
	for _, row := range overlay.inserts {
		if err := writeWALRecord(file, walInsert, overlay.base.namespace, overlay.base.name, row); err != nil {
			return err
		}
	}
	for _, primary := range sortedKeys(overlay.updates) {
		row := overlay.updates[primary]
		update := make([]string, 0, len(row)+1)
		update = append(update, primary)
		update = append(update, row...)
		if err := writeWALRecord(file, walUpdate, overlay.base.namespace, overlay.base.name, update); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func applyOverlay(target *table, overlay *tableOverlay) error {
	if overlay.clear {
		clearTable(target)
	}
	for _, primary := range sortedKeys(overlay.deletes) {
		if err := target.deletePrimary(primary); err != nil {
			return err
		}
	}
	for _, primary := range sortedKeys(overlay.updates) {
		position, ok := target.primaryIdx[primary]
		if !ok {
			return errMissingRow
		}
		if err := target.replaceRow(position, overlay.updates[primary]); err != nil {
			return err
		}
	}
	for _, row := range overlay.inserts {
		if err := target.appendRow(row); err != nil {
			return err
		}
	}
	return nil
}
