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
// Callers must pass an exclusive index map (see detachPrimaryIndexes) when
// appending so this can update the map in place without racing readers.
func MaintainPrimaryIndex(table *Table, previousLength int, previous map[string]int) {
	if previous == nil || len(table.Rows) < previousLength {
		RebuildPrimaryIndex(table)
		return
	}
	table.PrimaryIndex = previous
	if len(table.Rows) == previousLength {
		return
	}
	primary, _ := tableKeyColumns(*table)
	if len(primary) == 0 {
		return
	}
	indexes := columnPositions(*table, primary)
	for rowIndex := previousLength; rowIndex < len(table.Rows); rowIndex++ {
		table.PrimaryIndex[rowKey(table.Rows[rowIndex], indexes)] = rowIndex
	}
}

// detachPrimaryIndexes replaces shared primary-index maps with exclusive copies
// so later MaintainPrimaryIndex calls can append in place.
func detachPrimaryIndexes(definition Definition) {
	for namespaceKey, namespace := range definition.Namespaces {
		for tableKey, table := range namespace.Tables {
			if table.PrimaryIndex == nil {
				continue
			}
			owned := make(map[string]int, len(table.PrimaryIndex)+8)
			for key, value := range table.PrimaryIndex {
				owned[key] = value
			}
			table.PrimaryIndex = owned
			namespace.Tables[tableKey] = table
		}
		definition.Namespaces[namespaceKey] = namespace
	}
}
