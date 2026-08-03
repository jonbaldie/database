package catalog

import "fmt"

func (s *Store) replaceLocked(definition Definition) error {
	staged := cloneDefinition(definition)
	detachPrimaryIndexes(staged)
	if err := validateDefinition(staged); err != nil {
		return fmt.Errorf("invalid catalog: %w", err)
	}
	schemaChanged := !sameSchema(s.definition, staged)
	if err := s.syncRowsLocked(s.definition, staged); err != nil {
		return err
	}
	if schemaChanged {
		if err := s.persistLocked(staged); err != nil {
			return err
		}
	}
	s.definition = staged
	s.revision++
	return nil
}

type preparedRowSync struct {
	store *Store
	txns  []rowTxn
}

func (s *Store) prepareRowSync(previous, next Definition) (*preparedRowSync, error) {
	if s.rows == nil {
		return &preparedRowSync{store: s}, nil
	}
	prepared := &preparedRowSync{store: s}
	for namespaceKey, namespace := range next.Namespaces {
		if err := s.prepareNamespaceRowSync(prepared, previous.Namespaces[namespaceKey], namespace, namespaceKey); err != nil {
			return nil, err
		}
	}
	return prepared, nil
}

func (s *Store) prepareNamespaceRowSync(prepared *preparedRowSync, previousNamespace, namespace Namespace, namespaceKey string) error {
	namespaceName := namespace.Name
	if namespaceName == "" {
		namespaceName = namespaceKey
	}
	for tableKey, table := range namespace.Tables {
		tableName := table.Name
		if tableName == "" {
			tableName = tableKey
		}
		if err := s.ensureRowTable(namespaceName, table); err != nil {
			return err
		}
		previousTable := previousNamespace.Tables[tableKey]
		if sameRowSlice(previousTable.Rows, table.Rows) {
			continue
		}
		primary, _ := tableKeyColumns(table)
		txn, err := s.stageTableRows(namespaceName, tableName, previousTable.Rows, table.Rows, table, primary)
		if err != nil {
			return err
		}
		if txn != nil {
			prepared.txns = append(prepared.txns, txn)
		}
	}
	return nil
}

func (s *Store) stageTableRows(namespace, name string, previous, next [][]string, table Table, primary []string) (rowTxn, error) {
	txn, err := s.rows.Begin()
	if err != nil {
		return nil, err
	}
	switch {
	case len(primary) == 0:
		return txn, replaceAllRows(txn, namespace, name, next)
	case appendOnlyRowImage(previous, next):
		return txn, appendRows(txn, namespace, name, next[len(previous):])
	case inPlaceRowUpdates(previous, next, table, primary):
		return txn, updateChangedRows(txn, namespace, name, previous, next, table, primary)
	case len(next) == 0:
		return txn, txn.Clear(namespace, name)
	default:
		return txn, replaceAllRows(txn, namespace, name, next)
	}
}

func replaceAllRows(txn rowTxn, namespace, name string, rows [][]string) error {
	if err := txn.Clear(namespace, name); err != nil {
		return err
	}
	return appendRows(txn, namespace, name, rows)
}

func appendRows(txn rowTxn, namespace, name string, rows [][]string) error {
	for _, row := range rows {
		if err := txn.Insert(namespace, name, row); err != nil {
			return err
		}
	}
	return nil
}

func updateChangedRows(txn rowTxn, namespace, name string, previous, next [][]string, table Table, primary []string) error {
	primaryIndexes := columnPositions(table, primary)
	limit := len(previous)
	for index := 0; index < limit; index++ {
		if sameRowRef(previous[index], next[index]) || rowEqual(previous[index], next[index]) {
			continue
		}
		key := rowKey(previous[index], primaryIndexes)
		if err := txn.UpdatePrimary(namespace, name, key, next[index]); err != nil {
			return err
		}
	}
	return nil
}

func appendOnlyRowImage(previous, next [][]string) bool {
	if len(next) <= len(previous) {
		return false
	}
	if len(previous) == 0 {
		return true
	}
	last := len(previous) - 1
	if sameRowRef(previous[0], next[0]) && sameRowRef(previous[last], next[last]) {
		return true
	}
	previousLen := len(previous)
	for index := 0; index < previousLen; index++ {
		if !sameRowRef(previous[index], next[index]) && !rowEqual(previous[index], next[index]) {
			return false
		}
	}
	return true
}

func inPlaceRowUpdates(previous, next [][]string, table Table, primary []string) bool {
	if len(previous) != len(next) || len(primary) == 0 {
		return false
	}
	indexes := columnPositions(table, primary)
	previousLen := len(previous)
	for index := 0; index < previousLen; index++ {
		if rowKey(previous[index], indexes) != rowKey(next[index], indexes) {
			return false
		}
	}
	return true
}

func (p *preparedRowSync) commit() error {
	for _, txn := range p.txns {
		if err := txn.Commit(); err != nil {
			return err
		}
	}
	return nil
}
