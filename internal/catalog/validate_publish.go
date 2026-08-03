package catalog

import "fmt"

// validatePublishedDefinition checks structural shape and any installed SQL
// publish validator before a batch becomes durable.
func validatePublishedDefinition(store *Store, previous, next Definition) error {
	if !sameSchema(previous, next) {
		if err := validateDefinition(next); err != nil {
			return err
		}
	} else if err := validateChangedRows(previous, next); err != nil {
		return err
	}
	if store.publishValidator == nil {
		return nil
	}
	return store.publishValidator(previous, next)
}

func validateChangedRows(previous, next Definition) error {
	for namespaceKey, namespace := range next.Namespaces {
		previousNamespace := previous.Namespaces[namespaceKey]
		for tableKey, table := range namespace.Tables {
			previousTable := previousNamespace.Tables[tableKey]
			if sameRowSlice(previousTable.Rows, table.Rows) {
				continue
			}
			start := 0
			if appendOnlyRowImage(previousTable.Rows, table.Rows) {
				start = len(previousTable.Rows)
			}
			if err := validateTableRowsFrom(tableKey, table, start); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTableRowsFrom(tableName string, table Table, start int) error {
	rowCount := len(table.Rows)
	columnCount := len(table.Columns)
	for rowIndex := start; rowIndex < rowCount; rowIndex++ {
		if len(table.Rows[rowIndex]) != columnCount {
			return fmt.Errorf("table %q row %d has an invalid column count", tableName, rowIndex)
		}
	}
	return nil
}
