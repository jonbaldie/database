package catalog

// RebuildPrimaryIndex refreshes the in-memory primary-key map for point updates.
func RebuildPrimaryIndex(table *Table) {
	primary, _ := tableKeyColumns(*table)
	if len(primary) == 0 {
		table.PrimaryIndex = nil
		return
	}
	indexes := columnPositions(*table, primary)
	table.PrimaryIndex = make(map[string]int, len(table.Rows))
	for rowIndex, row := range table.Rows {
		table.PrimaryIndex[rowKey(row, indexes)] = rowIndex
	}
}

// EnsurePrimaryIndex returns a usable primary index, building it when absent.
func EnsurePrimaryIndex(table *Table) map[string]int {
	if table.PrimaryIndex != nil {
		return table.PrimaryIndex
	}
	RebuildPrimaryIndex(table)
	return table.PrimaryIndex
}

// MaintainPrimaryIndex updates the primary index after a row-image replacement.
// Published indexes are immutable and shared across snapshots, so appends drop
// the cached map instead of mutating it. Publish warms an exclusive index once
// per batch via warmPrimaryIndexes.
func MaintainPrimaryIndex(table *Table, previousLength int, previous map[string]int) {
	if len(table.Rows) < previousLength {
		RebuildPrimaryIndex(table)
		return
	}
	if len(table.Rows) == previousLength {
		table.PrimaryIndex = previous
		return
	}
	table.PrimaryIndex = nil
}
