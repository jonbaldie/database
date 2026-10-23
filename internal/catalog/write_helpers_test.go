package catalog

import (
	"errors"
	"fmt"
)

// These writes seed catalog-package tests. They publish through mutate, so
// they run structural catalog validation but not the SQL publish validator or
// table builder. Tests outside this package seed through SQL instead.

func (s *Store) createNamespace(name string) error {
	return s.mutate(func(definition *Definition) error {
		key := Key(name)
		if _, ok := definition.Namespaces[key]; ok {
			return errors.New("namespace already exists")
		}
		definition.Namespaces[key] = Namespace{Name: name, Tables: map[string]Table{}}
		return nil
	})
}

func (s *Store) createTable(namespace, name string, columns []string) error {
	return s.createTableWithTypes(namespace, name, columns, nil)
}

func (s *Store) createTableWithTypes(namespace, name string, columns, columnTypes []string) error {
	return s.mutate(func(definition *Definition) error {
		key := Key(namespace)
		ns, ok := definition.Namespaces[key]
		if !ok {
			return errors.New("namespace does not exist")
		}
		table := Key(name)
		if _, ok := ns.Tables[table]; ok {
			return errors.New("table already exists")
		}
		tableDefinition := Table{Name: name, Columns: append([]string(nil), columns...)}
		if len(columnTypes) > 0 {
			if len(columnTypes) != len(columns) {
				return errors.New("column type count does not match column count")
			}
			tableDefinition.ColumnTypes = append([]string(nil), columnTypes...)
		}
		ns.Tables[table] = tableDefinition
		definition.Namespaces[key] = ns
		return nil
	})
}

func (s *Store) insert(namespace, table string, row []string) error {
	return s.mutate(func(definition *Definition) error {
		ns, ok := definition.Namespaces[Key(namespace)]
		if !ok {
			return errors.New("namespace does not exist")
		}
		key := Key(table)
		tableDefinition, ok := ns.Tables[key]
		if !ok {
			return errors.New("table does not exist")
		}
		if len(row) != len(tableDefinition.Columns) {
			return errors.New("column count does not match value count")
		}
		tableDefinition.Rows = append(tableDefinition.Rows, append([]string(nil), row...))
		ns.Tables[key] = tableDefinition
		definition.Namespaces[Key(namespace)] = ns
		return nil
	})
}

func (s *Store) replaceRows(namespace, table string, rows [][]string) error {
	return s.mutate(func(definition *Definition) error {
		key := Key(namespace)
		ns, ok := definition.Namespaces[key]
		if !ok {
			return errors.New("namespace does not exist")
		}
		tableKey := Key(table)
		current, ok := ns.Tables[tableKey]
		if !ok {
			return errors.New("table does not exist")
		}
		for index, row := range rows {
			if len(row) != len(current.Columns) {
				return fmt.Errorf("row %d column count does not match table", index)
			}
		}
		current.Rows = cloneRows(rows)
		ns.Tables[tableKey] = current
		definition.Namespaces[key] = ns
		return nil
	})
}

func cloneRows(rows [][]string) [][]string {
	copy := make([][]string, len(rows))
	for index, row := range rows {
		copy[index] = append([]string(nil), row...)
	}
	return copy
}
