package storage

import "strings"

// LookupPrimary returns the row for a primary-key value.
func (e *Engine) LookupPrimary(namespace, name, key string) ([]string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	current, ok := e.tables[tableKey(namespace, name)]
	if !ok {
		return nil, false
	}
	position, ok := current.primaryIdx[key]
	if !ok {
		return nil, false
	}
	return append([]string(nil), current.rows[position]...), true
}

// LookupUnique returns the row for a single-column unique key.
func (e *Engine) LookupUnique(namespace, name, column, key string) ([]string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	current, ok := e.tables[tableKey(namespace, name)]
	if !ok {
		return nil, false
	}
	indexKey := column
	positions, ok := current.uniqueIdx[indexKey]
	if !ok {
		// Multi-column unique keys are joined with NUL in EnsureTable.
		positions, ok = current.uniqueIdx[strings.Join([]string{column}, "\x00")]
		if !ok {
			return nil, false
		}
	}
	position, ok := positions[key]
	if !ok {
		return nil, false
	}
	return append([]string(nil), current.rows[position]...), true
}

// RowCount reports durable rows in one table.
func (e *Engine) RowCount(namespace, name string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	current, ok := e.tables[tableKey(namespace, name)]
	if !ok {
		return 0
	}
	return len(current.rows)
}

// SnapshotRows returns a shallow-copied row list for compatibility scans.
func (e *Engine) SnapshotRows(namespace, name string) ([][]string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	current, ok := e.tables[tableKey(namespace, name)]
	if !ok {
		return nil, false
	}
	rows := make([][]string, len(current.rows))
	copy(rows, current.rows)
	return rows, true
}
