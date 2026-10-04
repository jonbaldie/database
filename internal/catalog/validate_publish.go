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
	return store.validateConstraints(previous, next)
}

// validateConstraints runs the installed SQL publish validator. It is the one
// authority for SQL constraints: transactions call it when they stage a
// mutation, and durable publication calls it again on the merged result.
func (s *Store) validateConstraints(previous, next Definition) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	validator := s.publishValidator
	s.mu.Unlock()
	if validator == nil {
		return nil
	}
	return validator(previous, next)
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
