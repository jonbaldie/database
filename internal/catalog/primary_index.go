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
func MaintainPrimaryIndex(table *Table, previousLength int, previous map[string]int) {
	// A same-length rewrite (REPLACE delete-then-append, or an in-place
	// primary-key change) can move or rekey existing rows. Only a strict
	// suffix append can reuse the previous map.
	if previous == nil || len(table.Rows) <= previousLength {
		RebuildPrimaryIndex(table)
		return
	}
	table.PrimaryIndex = clonePrimaryIndex(previous, len(table.Rows)-previousLength)
	if len(table.Rows) == previousLength {
		return
	}
	primary, _ := tableKeyColumns(*table)
	if len(primary) == 0 {
		return
	}
	indexes := columnPositions(*table, primary)
	limit := len(table.Rows)
	for rowIndex := previousLength; rowIndex < limit; rowIndex++ {
		table.PrimaryIndex[rowKey(table.Rows[rowIndex], indexes)] = rowIndex
	}
}

func clonePrimaryIndex(index map[string]int, growth int) map[string]int {
	owned := make(map[string]int, len(index)+growth)
	for key, value := range index {
		owned[key] = value
	}
	return owned
}
