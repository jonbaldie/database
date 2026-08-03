package catalog

import "fmt"

func (s *Store) replaceLocked(definition Definition) error {
	staged := cloneDefinition(definition)
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
	warmPrimaryIndexes(staged)
	s.definition = staged
	s.revision++
	return nil
}

func warmPrimaryIndexes(definition Definition) {
	for namespaceKey, namespace := range definition.Namespaces {
		for tableKey, table := range namespace.Tables {
			if table.PrimaryIndex != nil {
				continue
			}
			RebuildPrimaryIndex(&table)
			namespace.Tables[tableKey] = table
		}
		definition.Namespaces[namespaceKey] = namespace
	}
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
		namespaceName := namespace.Name
		if namespaceName == "" {
			namespaceName = namespaceKey
		}
		previousNamespace := previous.Namespaces[namespaceKey]
		for tableKey, table := range namespace.Tables {
			tableName := table.Name
			if tableName == "" {
				tableName = tableKey
			}
			if err := s.ensureRowTable(namespaceName, table); err != nil {
				return nil, err
			}
			previousTable := previousNamespace.Tables[tableKey]
			if sameRowSlice(previousTable.Rows, table.Rows) {
				continue
			}
			primary, _ := tableKeyColumns(table)
			txn, err := s.stageTableRows(namespaceName, tableName, previousTable.Rows, table.Rows, table, primary)
			if err != nil {
				return nil, err
			}
			if txn != nil {
				prepared.txns = append(prepared.txns, txn)
			}
		}
	}
	return prepared, nil
}

func (s *Store) stageTableRows(namespace, name string, previous, next [][]string, table Table, primary []string) (rowTxn, error) {
	txn, err := s.rows.Begin()
	if err != nil {
		return nil, err
	}
	if appendOnlyRowImage(previous, next) {
		for index := len(previous); index < len(next); index++ {
			if err := txn.Insert(namespace, name, next[index]); err != nil {
				return nil, err
			}
		}
		return txn, nil
	}
	if inPlaceRowUpdates(previous, next) {
		primaryIndexes := columnPositions(table, primary)
		for index := range previous {
			if sameRowRef(previous[index], next[index]) || rowEqual(previous[index], next[index]) {
				continue
			}
			key := rowKey(next[index], primaryIndexes)
			if err := txn.UpdatePrimary(namespace, name, key, next[index]); err != nil {
				return nil, err
			}
		}
		return txn, nil
	}
	if len(next) == 0 {
		if err := txn.Clear(namespace, name); err != nil {
			return nil, err
		}
		return txn, nil
	}
	if err := txn.Clear(namespace, name); err != nil {
		return nil, err
	}
	for _, row := range next {
		if err := txn.Insert(namespace, name, row); err != nil {
			return nil, err
		}
	}
	return txn, nil
}

func appendOnlyRowImage(previous, next [][]string) bool {
	if len(next) <= len(previous) {
		return false
	}
	if len(previous) == 0 {
		return true
	}
	if sameRowRef(previous[0], next[0]) && sameRowRef(previous[len(previous)-1], next[len(previous)-1]) {
		return true
	}
	for index := range previous {
		if !sameRowRef(previous[index], next[index]) && !rowEqual(previous[index], next[index]) {
			return false
		}
	}
	return true
}

func inPlaceRowUpdates(previous, next [][]string) bool {
	if len(previous) != len(next) {
		return false
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
