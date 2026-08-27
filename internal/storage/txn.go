package storage

import (
	"io"
	"os"
	"sort"
	"strings"
)

type tableOverlay struct {
	base        *table
	baseVersion uint64
	inserts     [][]string
	insertKeys  map[string]struct{}
	uniqueKeys  map[string]map[string]struct{}
	updates     map[string][]string
	deletes     map[string]bool
	clear       bool
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
	if err := txn.lockOverlay(overlay); err != nil {
		return err
	}
	defer txn.engine.mu.RUnlock()
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
	staged := append([]string(nil), row...)
	overlay.inserts = append(overlay.inserts, staged)
	overlay.addInsertKeys(staged)
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
	if _, exists := overlay.insertKeys[key]; exists {
		return errDuplicateKey
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
	indexKey := strings.Join(unique, "\x00")
	if _, exists := overlay.uniqueKeys[indexKey][uniqueKey]; exists {
		return errDuplicateKey
	}
	return nil
}

func (overlay *tableOverlay) addInsertKeys(row []string) {
	if len(overlay.base.primary) > 0 {
		if overlay.insertKeys == nil {
			overlay.insertKeys = map[string]struct{}{}
		}
		overlay.insertKeys[overlay.base.primaryKey(row)] = struct{}{}
	}
	for _, unique := range overlay.base.uniques {
		indexes := overlay.base.columnIndexes(unique)
		if uniqueKeyNullable(row, indexes) {
			continue
		}
		indexKey := strings.Join(unique, "\x00")
		if overlay.uniqueKeys == nil {
			overlay.uniqueKeys = map[string]map[string]struct{}{}
		}
		if overlay.uniqueKeys[indexKey] == nil {
			overlay.uniqueKeys[indexKey] = map[string]struct{}{}
		}
		overlay.uniqueKeys[indexKey][rowKey(row, indexes)] = struct{}{}
	}
}

func (overlay *tableOverlay) removeInsertKeys(row []string) {
	if len(overlay.base.primary) > 0 {
		delete(overlay.insertKeys, overlay.base.primaryKey(row))
	}
	for _, unique := range overlay.base.uniques {
		indexes := overlay.base.columnIndexes(unique)
		if uniqueKeyNullable(row, indexes) {
			continue
		}
		indexKey := strings.Join(unique, "\x00")
		delete(overlay.uniqueKeys[indexKey], rowKey(row, indexes))
	}
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
	if err := txn.lockOverlay(overlay); err != nil {
		return err
	}
	defer txn.engine.mu.RUnlock()
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
	if err := txn.lockOverlay(overlay); err != nil {
		return err
	}
	defer txn.engine.mu.RUnlock()
	overlay.clear = true
	overlay.inserts = nil
	overlay.insertKeys = nil
	overlay.uniqueKeys = nil
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
	if err := txn.lockOverlay(overlay); err != nil {
		return err
	}
	defer txn.engine.mu.RUnlock()
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
			overlay.removeInsertKeys(row)
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
	var version uint64
	if ok {
		version = base.version
	}
	txn.engine.mu.RUnlock()
	if !ok {
		return nil, errMissingTable
	}
	staged := &tableOverlay{base: base, baseVersion: version}
	txn.overlays[key] = staged
	return staged, nil
}

func (txn *Transaction) lockOverlay(overlay *tableOverlay) error {
	txn.engine.mu.RLock()
	current, ok := txn.engine.tables[tableKey(overlay.base.namespace, overlay.base.name)]
	if !ok || current != overlay.base || current.version != overlay.baseVersion {
		txn.engine.mu.RUnlock()
		return errMissingTable
	}
	return nil
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
	if err := validateOverlays(engine, txn.overlays, keys); err != nil {
		return err
	}
	if err := appendTransactionWAL(engine.wal, txn.overlays, keys); err != nil {
		return err
	}
	if err := publishCommittedOverlays(engine, txn.overlays, keys); err != nil {
		return err
	}
	txn.finished = true
	return nil
}

func ensureCommitReady(engine *Engine) error {
	if engine.closed || engine.wal == nil {
		return errClosed
	}
	return nil
}

func validateOverlays(engine *Engine, overlays map[string]*tableOverlay, keys []string) error {
	for _, key := range keys {
		overlay := overlays[key]
		current, ok := engine.tables[key]
		if !ok || current != overlay.base || current.version != overlay.baseVersion {
			return errMissingTable
		}
		if err := validateOverlay(current, overlay); err != nil {
			return err
		}
	}
	return nil
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

func publishCommittedOverlays(engine *Engine, overlays map[string]*tableOverlay, keys []string) error {
	for _, key := range keys {
		current := engine.tables[key]
		if err := applyOverlay(current, overlays[key]); err != nil {
			return err
		}
		current.version++
	}
	return nil
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

type overlayConstraintState struct {
	table          *table
	clear          bool
	removedPrimary map[string]bool
	addedPrimary   map[string]bool
	removedUnique  map[string]map[string]bool
	addedUnique    map[string]map[string]bool
}

func validateOverlay(current *table, overlay *tableOverlay) error {
	state := newOverlayConstraintState(current, overlay.clear)
	for _, primary := range sortedKeys(overlay.deletes) {
		position, ok := current.primaryIdx[primary]
		if !ok {
			return errMissingRow
		}
		state.remove(current.rows[position])
	}
	for _, primary := range sortedKeys(overlay.updates) {
		position, ok := current.primaryIdx[primary]
		if !ok {
			return errMissingRow
		}
		state.remove(current.rows[position])
		if err := state.add(overlay.updates[primary]); err != nil {
			return err
		}
	}
	for _, row := range overlay.inserts {
		if err := state.add(row); err != nil {
			return err
		}
	}
	return nil
}

func newOverlayConstraintState(current *table, clear bool) *overlayConstraintState {
	state := &overlayConstraintState{
		table: current, clear: clear,
		removedPrimary: map[string]bool{}, addedPrimary: map[string]bool{},
		removedUnique: map[string]map[string]bool{}, addedUnique: map[string]map[string]bool{},
	}
	for _, unique := range current.uniques {
		key := strings.Join(unique, "\x00")
		state.removedUnique[key] = map[string]bool{}
		state.addedUnique[key] = map[string]bool{}
	}
	return state
}

func (state *overlayConstraintState) remove(row []string) {
	if len(state.table.primary) > 0 {
		state.removedPrimary[state.table.primaryKey(row)] = true
	}
	for _, unique := range state.table.uniques {
		indexes := state.table.columnIndexes(unique)
		if uniqueKeyNullable(row, indexes) {
			continue
		}
		indexKey := strings.Join(unique, "\x00")
		state.removedUnique[indexKey][rowKey(row, indexes)] = true
	}
}

func (state *overlayConstraintState) add(row []string) error {
	if err := state.table.validateRow(row); err != nil {
		return err
	}
	if len(state.table.primary) > 0 {
		key := state.table.primaryKey(row)
		if state.primaryExists(key) {
			return errDuplicateKey
		}
		state.addedPrimary[key] = true
	}
	for _, unique := range state.table.uniques {
		indexes := state.table.columnIndexes(unique)
		if uniqueKeyNullable(row, indexes) {
			continue
		}
		indexKey := strings.Join(unique, "\x00")
		key := rowKey(row, indexes)
		if state.uniqueExists(indexKey, key) {
			return errDuplicateKey
		}
		state.addedUnique[indexKey][key] = true
	}
	return nil
}

func (state *overlayConstraintState) primaryExists(key string) bool {
	if state.addedPrimary[key] {
		return true
	}
	if state.clear || state.removedPrimary[key] {
		return false
	}
	_, exists := state.table.primaryIdx[key]
	return exists
}

func (state *overlayConstraintState) uniqueExists(indexKey, key string) bool {
	if state.addedUnique[indexKey][key] {
		return true
	}
	if state.clear || state.removedUnique[indexKey][key] {
		return false
	}
	_, exists := state.table.uniqueIdx[indexKey][key]
	return exists
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
