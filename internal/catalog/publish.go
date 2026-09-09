package catalog

import "fmt"

func (s *Store) replaceLocked(definition Definition) error {
	staged := cloneDefinition(definition)
	refreshOrderedIndexCaches(s.definition, &staged)
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
	store         *Store
	txns          []rowTxn
	drops         []droppedTable
	schemaChanged bool
}

// droppedTable names one table whose durable row image must disappear with its
// catalog entry, so a later table of the same name starts empty.
type droppedTable struct {
	namespace string
	name      string
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
	collectDroppedTables(prepared, previous, next)
	return prepared, nil
}

func collectDroppedTables(prepared *preparedRowSync, previous, next Definition) {
	for namespaceKey, namespace := range previous.Namespaces {
		nextNamespace := next.Namespaces[namespaceKey]
		namespaceName := namespace.Name
		if namespaceName == "" {
			namespaceName = namespaceKey
		}
		for tableKey, table := range namespace.Tables {
			if _, kept := nextNamespace.Tables[tableKey]; kept {
				continue
			}
			tableName := table.Name
			if tableName == "" {
				tableName = tableKey
			}
			prepared.drops = append(prepared.drops, droppedTable{namespace: namespaceName, name: tableName})
		}
	}
}

func (s *Store) prepareNamespaceRowSync(prepared *preparedRowSync, previousNamespace, namespace Namespace, namespaceKey string) error {
	namespaceName := namespace.Name
	if namespaceName == "" {
		namespaceName = namespaceKey
	}
	for tableKey, table := range namespace.Tables {
		if err := s.prepareTableRowSync(prepared, previousNamespace, namespaceName, tableKey, table); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) prepareTableRowSync(prepared *preparedRowSync, previousNamespace Namespace, namespaceName, tableKey string, table Table) error {
	schemaChanged := prepareRowSyncSchemaChange(prepared, previousNamespace, tableKey, table)
	rowsChanged := !sameRowSlice(previousNamespace.Tables[tableKey].Rows, table.Rows)
	if !schemaChanged && !rowsChanged {
		return nil
	}
	if err := s.ensureRowTable(namespaceName, table); err != nil || !rowsChanged {
		return err
	}
	tableName := table.Name
	if tableName == "" {
		tableName = tableKey
	}
	primary, _ := tableKeyColumns(table)
	txn, err := s.stageTableRows(namespaceName, tableName, previousNamespace.Tables[tableKey].Rows, table.Rows, table, primary)
	if err == nil && txn != nil {
		prepared.txns = append(prepared.txns, txn)
	}
	return err
}

func sameRowStorageSchema(left, right Table) bool {
	leftPrimary, leftUniques := tableKeyColumns(left)
	rightPrimary, rightUniques := tableKeyColumns(right)
	return sameCatalogStrings(left.Columns, right.Columns) && sameCatalogStrings(leftPrimary, rightPrimary) && sameCatalogStringMatrix(leftUniques, rightUniques)
}

// prepareRowSyncSchemaChange records whether the table's durable row schema is
// about to change, so the commit can rebuild the row image from scratch.
func prepareRowSyncSchemaChange(prepared *preparedRowSync, previousNamespace Namespace, tableKey string, table Table) bool {
	previousTable, existed := previousNamespace.Tables[tableKey]
	if existed && sameRowStorageSchema(previousTable, table) {
		return false
	}
	prepared.schemaChanged = true
	return true
}

func sameCatalogStringMatrix(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !sameCatalogStrings(left[index], right[index]) {
			return false
		}
	}
	return true
}

func sameCatalogStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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
	for _, dropped := range p.drops {
		if err := p.store.rows.DropTable(dropped.namespace, dropped.name); err != nil {
			return err
		}
	}
	// A schema change leaves historical WAL records whose rows no longer match
	// the live column list, so replay must start from the current row image.
	if p.schemaChanged {
		return p.store.rows.Checkpoint()
	}
	return nil
}
