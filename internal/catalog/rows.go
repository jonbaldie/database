package catalog

import (
	"strings"

	"github.com/jonbaldie/database/internal/storage"
)

type storageRows struct {
	engine *storage.Engine
}

type storageTxn struct {
	txn *storage.Transaction
}

func openWithRows(store *Store, directory string) (*Store, error) {
	engine, err := storage.Open(directory)
	if err != nil {
		return nil, err
	}
	store.rows = storageRows{engine: engine}
	if err := store.hydrateRowsFromEngine(); err != nil {
		_ = engine.Close()
		return nil, err
	}
	return store, nil
}

func (s storageRows) EnsureTable(namespace, name string, columns, primary []string, uniques [][]string) error {
	return s.engine.EnsureTable(namespace, name, columns, primary, uniques)
}

func (s storageRows) Begin() (rowTxn, error) {
	txn, err := s.engine.Begin()
	if err != nil {
		return nil, err
	}
	return storageTxn{txn: txn}, nil
}

func (s storageRows) LookupPrimary(namespace, name, key string) ([]string, bool) {
	return s.engine.LookupPrimary(namespace, name, key)
}

func (s storageRows) LookupUnique(namespace, name, column, key string) ([]string, bool) {
	return s.engine.LookupUnique(namespace, name, column, key)
}

func (s storageRows) SnapshotRows(namespace, name string) ([][]string, bool) {
	return s.engine.SnapshotRows(namespace, name)
}

func (s storageRows) Close() error {
	return s.engine.Close()
}

func (t storageTxn) Insert(namespace, name string, row []string) error {
	return t.txn.Insert(namespace, name, row)
}

func (t storageTxn) UpdatePrimary(namespace, name, primary string, row []string) error {
	return t.txn.UpdatePrimary(namespace, name, primary, row)
}

func (t storageTxn) DeletePrimary(namespace, name, primary string) error {
	return t.txn.DeletePrimary(namespace, name, primary)
}

func (t storageTxn) Clear(namespace, name string) error {
	return t.txn.Clear(namespace, name)
}

func (t storageTxn) Commit() error {
	return t.txn.Commit()
}

func (s *Store) hydrateRowsFromEngine() error {
	if s.rows == nil {
		return nil
	}
	for namespaceKey, namespace := range s.definition.Namespaces {
		for tableKey, table := range namespace.Tables {
			namespaceName, tableName := resolvedTableNames(namespaceKey, namespace, tableKey, table)
			updated, err := s.hydrateTableRows(namespaceName, tableName, table)
			if err != nil {
				return err
			}
			namespace.Tables[tableKey] = updated
		}
		s.definition.Namespaces[namespaceKey] = namespace
	}
	return nil
}

func resolvedTableNames(namespaceKey string, namespace Namespace, tableKey string, table Table) (string, string) {
	namespaceName := namespace.Name
	if namespaceName == "" {
		namespaceName = namespaceKey
	}
	tableName := table.Name
	if tableName == "" {
		tableName = tableKey
	}
	return namespaceName, tableName
}

func (s *Store) hydrateTableRows(namespaceName, tableName string, table Table) (Table, error) {
	if err := s.ensureRowTable(namespaceName, table); err != nil {
		return table, err
	}
	rows, ok := s.rows.SnapshotRows(namespaceName, tableName)
	if !ok || len(rows) == 0 {
		if len(table.Rows) > 0 {
			if err := s.seedRowsLocked(namespaceName, tableName, table); err != nil {
				return table, err
			}
		}
		RebuildPrimaryIndex(&table)
		return table, nil
	}
	table.Rows = rows
	RebuildPrimaryIndex(&table)
	return table, nil
}

func (s *Store) seedRowsLocked(namespace, name string, table Table) error {
	txn, err := s.rows.Begin()
	if err != nil {
		return err
	}
	if err := txn.Clear(namespace, name); err != nil {
		return err
	}
	for _, row := range table.Rows {
		if err := txn.Insert(namespace, name, row); err != nil {
			return err
		}
	}
	return txn.Commit()
}

func (s *Store) ensureRowTable(namespace string, table Table) error {
	if s.rows == nil {
		return nil
	}
	primary, uniques := tableKeyColumns(table)
	return s.rows.EnsureTable(namespace, table.Name, table.Columns, primary, uniques)
}

func tableKeyColumns(table Table) ([]string, [][]string) {
	primary, uniques := constraintKeyColumns(table)
	uniques = append(uniques, uniqueIndexKeyColumns(table)...)
	return primary, dedupeKeyLists(uniques)
}

func constraintKeyColumns(table Table) ([]string, [][]string) {
	primary := []string{}
	uniques := [][]string{}
	for _, constraint := range table.Constraints {
		switch constraint.Type {
		case ConstraintTypePrimary:
			primary = append([]string(nil), constraint.Columns...)
		case ConstraintTypeUnique:
			uniques = append(uniques, append([]string(nil), constraint.Columns...))
		}
	}
	return primary, uniques
}

func uniqueIndexKeyColumns(table Table) [][]string {
	uniques := [][]string{}
	for _, index := range table.Indexes {
		if !index.Unique || len(index.Parts) == 0 {
			continue
		}
		columns := uniqueIndexColumns(index)
		if columns != nil {
			uniques = append(uniques, columns)
		}
	}
	return uniques
}

func uniqueIndexColumns(index Index) []string {
	columns := make([]string, 0, len(index.Parts))
	for _, part := range index.Parts {
		if part.Column == "" {
			return nil
		}
		columns = append(columns, part.Column)
	}
	return columns
}

func dedupeKeyLists(values [][]string) [][]string {
	seen := map[string]bool{}
	out := make([][]string, 0, len(values))
	for _, value := range values {
		key := strings.Join(value, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

// Rows exposes the durable row engine for point lookups.
func (s *Store) Rows() rowEngine {
	return s.rows
}
