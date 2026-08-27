package mysql

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

// validateConstraintDefinition validates both the schema and every durable row
// image. mutateCatalog calls it before a transaction snapshot or catalog file
// becomes visible, so a failed write or DDL change is atomic.
func validateConstraintDefinition(previous, definition catalog.Definition) error {
	changed := changedPublishedTables(previous, definition)
	return validateChangedTableConstraints(previous, definition, changed)
}

func changedPublishedTables(previous, definition catalog.Definition) map[string]bool {
	changed := map[string]bool{}
	for namespaceKey, namespace := range definition.Namespaces {
		previousNamespace := previous.Namespaces[namespaceKey]
		for tableKey, table := range namespace.Tables {
			previousTable := previousNamespace.Tables[tableKey]
			if samePublishedRows(previousTable.Rows, table.Rows) && sameTableShape(previousTable, table) {
				continue
			}
			changed[namespaceKey+"\x00"+tableKey] = true
		}
	}
	return changed
}

func validateChangedTableConstraints(previous, definition catalog.Definition, changed map[string]bool) error {
	for namespaceKey, namespace := range definition.Namespaces {
		namespaceName := namespace.Name
		if namespaceName == "" {
			namespaceName = namespaceKey
		}
		for tableKey, table := range namespace.Tables {
			if !changed[namespaceKey+"\x00"+tableKey] && !foreignKeyDependsOnChanged(namespaceKey, table, changed) {
				continue
			}
			tableName := table.Name
			if tableName == "" {
				tableName = tableKey
			}
			if err := validateTableConstraints(previous, definition, namespaceName, tableName, table); err != nil {
				return err
			}
		}
	}
	return nil
}

func foreignKeyDependsOnChanged(namespaceKey string, table catalog.Table, changed map[string]bool) bool {
	for _, constraint := range table.Constraints {
		if constraint.Type != catalog.ConstraintTypeForeignKey {
			continue
		}
		referencedNamespace := catalog.Key(constraint.ReferencedNamespace)
		if referencedNamespace == "" {
			referencedNamespace = namespaceKey
		}
		if changed[referencedNamespace+"\x00"+catalog.Key(constraint.ReferencedTable)] {
			return true
		}
	}
	return false
}

func samePublishedRows(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	return &left[0] == &right[0]
}

func sameTableShape(left, right catalog.Table) bool {
	if len(left.Columns) != len(right.Columns) || len(left.Constraints) != len(right.Constraints) || len(left.Indexes) != len(right.Indexes) {
		return false
	}
	return true
}

func validateTableConstraints(previous, definition catalog.Definition, namespaceName, tableName string, table catalog.Table) error {
	if err := validateTableIndexes(table); err != nil {
		return err
	}
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return err
	}
	if err := validateConstraintDeclarations(previous, definition, namespaceName, tableName, table, indexes); err != nil {
		return err
	}
	previousTable := previous.Namespaces[catalog.Key(namespaceName)].Tables[catalog.Key(tableName)]
	if err := validateUniqueIndexesAgainstPrevious(previousTable, table, indexes); err != nil {
		return err
	}
	return validateNotNullColumnsAgainstPrevious(previousTable, table, indexes)
}

func validateUniqueIndexesAgainstPrevious(previous, table catalog.Table, columns map[string]int) error {
	for _, index := range table.Indexes {
		if !index.Unique {
			continue
		}
		if uniqueIndexUnchanged(previous, table, index, columns) {
			continue
		}
		if err := validateUniqueIndex(table, index, columns); err != nil {
			return err
		}
	}
	return nil
}

func uniqueIndexUnchanged(previous, table catalog.Table, index catalog.Index, columns map[string]int) bool {
	if len(previous.Rows) != len(table.Rows) {
		return false
	}
	for rowIndex := range table.Rows {
		if sameRowRef(previous.Rows[rowIndex], table.Rows[rowIndex]) {
			continue
		}
		before, beforeNull, err := uniqueIndexRowKey(table, index, columns, previous.Rows[rowIndex])
		if err != nil {
			return false
		}
		after, afterNull, err := uniqueIndexRowKey(table, index, columns, table.Rows[rowIndex])
		if err != nil || beforeNull != afterNull || before != after {
			return false
		}
	}
	return true
}

func validateNotNullColumnsAgainstPrevious(previous, table catalog.Table, indexes map[string]int) error {
	if isAppendOnlyRows(previous.Rows, table.Rows) {
		subset := table
		subset.Rows = table.Rows[len(previous.Rows):]
		return validateNotNullColumns(subset, indexes)
	}
	if len(previous.Rows) == len(table.Rows) {
		changed := false
		for index := range table.Rows {
			if !sameRowRef(previous.Rows[index], table.Rows[index]) {
				changed = true
				if err := validateNotNullColumns(catalog.Table{Columns: table.Columns, ColumnAttributes: table.ColumnAttributes, Constraints: table.Constraints, Rows: [][]string{table.Rows[index]}}, indexes); err != nil {
					return err
				}
			}
		}
		if changed {
			return nil
		}
	}
	return validateNotNullColumns(table, indexes)
}

func validateConstraintDeclarations(previous, definition catalog.Definition, namespaceName, tableName string, table catalog.Table, indexes map[string]int) error {
	seen := map[string]bool{}
	for _, constraint := range table.Constraints {
		if err := validateConstraintDeclaration(seen, previous, definition, namespaceName, tableName, table, constraint, indexes); err != nil {
			return err
		}
	}
	return nil
}

func validateConstraintDeclaration(seen map[string]bool, previous, definition catalog.Definition, namespaceName, tableName string, table catalog.Table, constraint catalog.Constraint, indexes map[string]int) error {
	if constraint.Name == "" || constraint.Type == "" {
		return errorsConstraintDefinition("constraint requires a name and type")
	}
	if seen[catalog.Key(constraint.Name)] {
		return errorsConstraintDefinition("duplicate constraint name '" + constraint.Name + "'")
	}
	seen[catalog.Key(constraint.Name)] = true
	if err := validateConstraintColumns(constraint, indexes); err != nil {
		return err
	}
	return validateConstraintRows(previous, definition, namespaceName, tableName, table, constraint, indexes)
}

func validateConstraintRows(previous, definition catalog.Definition, namespaceName, tableName string, table catalog.Table, constraint catalog.Constraint, indexes map[string]int) error {
	previousTable := previous.Namespaces[catalog.Key(namespaceName)].Tables[catalog.Key(tableName)]
	switch constraint.Type {
	case catalog.ConstraintTypePrimary, catalog.ConstraintTypeUnique:
		return validateUniqueConstraintAgainstPrevious(previousTable, table, constraint, indexes)
	case catalog.ConstraintTypeCheck:
		return validateCheckConstraint(namespaceName, tableName, table, constraint)
	case catalog.ConstraintTypeForeignKey:
		return validateForeignKeyConstraint(previous, definition, namespaceName, tableName, table, constraint, indexes)
	default:
		return errorsConstraintDefinition("unknown constraint type '" + constraint.Type + "'")
	}
}

func errorsConstraintDefinition(message string) error { return sqlFailure{3813, "HY000", message} }

func validateConstraintColumns(constraint catalog.Constraint, indexes map[string]int) error {
	if constraint.Type == catalog.ConstraintTypeCheck {
		if strings.TrimSpace(constraint.Check) == "" {
			return errorsConstraintDefinition("CHECK constraint '" + constraint.Name + "' has no expression")
		}
		return nil
	}
	if len(constraint.Columns) == 0 {
		return errorsConstraintDefinition("constraint '" + constraint.Name + "' has no columns")
	}
	for _, column := range constraint.Columns {
		if _, found := indexes[catalog.Key(column)]; !found {
			return errorsConstraintDefinition("constraint '" + constraint.Name + "' names unknown column '" + column + "'")
		}
	}
	return nil
}

func validateNotNullColumns(table catalog.Table, indexes map[string]int) error {
	primary := map[int]bool{}
	for _, constraint := range table.Constraints {
		if constraint.Type != catalog.ConstraintTypePrimary {
			continue
		}
		for _, column := range constraint.Columns {
			primary[indexes[catalog.Key(column)]] = true
		}
	}
	for _, row := range table.Rows {
		for index, value := range row {
			if value != storedSQLNullValue || (catalog.ColumnAttributeAt(table, index).Nullable && !primary[index]) {
				continue
			}
			return sqlFailure{1048, "23000", fmt.Sprintf("Column '%s' cannot be null", table.Columns[index])}
		}
	}
	return nil
}

func validateUniqueConstraint(table catalog.Table, constraint catalog.Constraint, indexes map[string]int) error {
	columns := constraintIndexes(constraint.Columns, indexes)
	seen := make(map[string]bool, len(table.Rows))
	for _, row := range table.Rows {
		key, nullable := constraintRowKey(table, row, columns)
		if nullable && constraint.Type == catalog.ConstraintTypeUnique {
			continue
		}
		if seen[key] {
			return sqlFailure{1062, "23000", "Duplicate entry for key '" + constraint.Name + "'"}
		}
		seen[key] = true
	}
	return nil
}

func validateUniqueConstraintAgainstPrevious(previous, table catalog.Table, constraint catalog.Constraint, indexes map[string]int) error {
	if isAppendOnlyRows(previous.Rows, table.Rows) {
		// The durable row engine rejects duplicate primary/unique keys when the
		// batch is staged, so an O(n) rebuild here only burns publish latency.
		return validateUniqueConstraintAppendNewKeys(previous, table, constraint, indexes)
	}
	if constraintExists(previous, constraint) && uniqueColumnsUnchanged(previous, table, constraint, indexes) {
		return nil
	}
	return validateUniqueConstraint(table, constraint, indexes)
}

func constraintExists(table catalog.Table, constraint catalog.Constraint) bool {
	for _, candidate := range table.Constraints {
		if candidate.Name == constraint.Name && candidate.Type == constraint.Type {
			return true
		}
	}
	return false
}

func validateUniqueConstraintAppendNewKeys(previous, table catalog.Table, constraint catalog.Constraint, indexes map[string]int) error {
	columns := constraintIndexes(constraint.Columns, indexes)
	newRows := table.Rows[len(previous.Rows):]
	if constraint.Type == catalog.ConstraintTypePrimary {
		return validateAppendPrimaryKeys(previous, table, constraint, columns, newRows)
	}
	return validateAppendUniqueKeys(previous, table, constraint, columns, newRows)
}

func validateAppendPrimaryKeys(previous, table catalog.Table, constraint catalog.Constraint, columns []int, newRows [][]string) error {
	seen := make(map[string]bool, len(newRows))
	primaryIndex := previous.PrimaryIndex
	for _, row := range newRows {
		key, nullable := constraintRowKey(table, row, columns)
		if nullable {
			continue
		}
		if seen[key] {
			return sqlFailure{1062, "23000", "Duplicate entry for key '" + constraint.Name + "'"}
		}
		if primaryIndex != nil {
			if _, exists := primaryIndex[key]; exists {
				return sqlFailure{1062, "23000", "Duplicate entry for key '" + constraint.Name + "'"}
			}
		}
		seen[key] = true
	}
	return nil
}

func validateAppendUniqueKeys(previous, table catalog.Table, constraint catalog.Constraint, columns []int, newRows [][]string) error {
	seen := make(map[string]bool, len(newRows))
	existing := existingUniqueKeys(previous, table, columns)
	for _, row := range newRows {
		key, nullable := constraintRowKey(table, row, columns)
		if nullable {
			continue
		}
		if existing[key] || seen[key] {
			return sqlFailure{1062, "23000", "Duplicate entry for key '" + constraint.Name + "'"}
		}
		seen[key] = true
	}
	return nil
}

func existingUniqueKeys(previous, table catalog.Table, columns []int) map[string]bool {
	existing := make(map[string]bool, len(previous.Rows))
	for _, row := range previous.Rows {
		key, nullable := constraintRowKey(table, row, columns)
		if nullable {
			continue
		}
		existing[key] = true
	}
	return existing
}

func uniqueColumnsUnchanged(previous, table catalog.Table, constraint catalog.Constraint, indexes map[string]int) bool {
	if len(previous.Rows) != len(table.Rows) {
		return false
	}
	columns := constraintIndexes(constraint.Columns, indexes)
	for index := range table.Rows {
		if sameRowRef(previous.Rows[index], table.Rows[index]) {
			continue
		}
		before, beforeNull := constraintRowKey(table, previous.Rows[index], columns)
		after, afterNull := constraintRowKey(table, table.Rows[index], columns)
		if beforeNull != afterNull || before != after {
			return false
		}
	}
	return true
}

func sameRowRef(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	return &left[0] == &right[0]
}

func isAppendOnlyRows(previous, next [][]string) bool {
	if len(next) <= len(previous) {
		return false
	}
	if len(previous) == 0 {
		return true
	}
	// applyInsertPlan copy-on-write keeps every previous row reference in order.
	// Checking the endpoints is enough for that path; other writers fall back.
	if sameRowRef(previous[0], next[0]) && sameRowRef(previous[len(previous)-1], next[len(previous)-1]) {
		return true
	}
	for index := range previous {
		if !sameRowContents(previous[index], next[index]) {
			return false
		}
	}
	return true
}

func sameRowContents(left, right []string) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	if len(left) == 0 || len(right) == 0 || len(left) != len(right) {
		return false
	}
	if &left[0] == &right[0] {
		return true
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateUniqueIndexes(table catalog.Table, columns map[string]int) error {
	for _, index := range table.Indexes {
		if !index.Unique {
			continue
		}
		if err := validateUniqueIndex(table, index, columns); err != nil {
			return err
		}
	}
	return nil
}

func validateUniqueIndex(table catalog.Table, index catalog.Index, columns map[string]int) error {
	seen := map[string]bool{}
	for _, row := range table.Rows {
		key, nullable, err := uniqueIndexRowKey(table, index, columns, row)
		if err != nil {
			return err
		}
		if nullable {
			continue
		}
		if seen[key] {
			return sqlFailure{1062, "23000", "Duplicate entry for key '" + index.Name + "'"}
		}
		seen[key] = true
	}
	return nil
}

func uniqueIndexRowKey(table catalog.Table, index catalog.Index, columnIndexes map[string]int, row []string) (string, bool, error) {
	parts := make([]string, len(index.Parts))
	columns := relationalTableColumns("", table.Name, table.Name, table)
	for number, part := range index.Parts {
		key, nullable, err := uniqueIndexPartKey(table, part, columnIndexes, columns, row)
		if err != nil || nullable {
			return "", nullable, err
		}
		parts[number] = strconv.Itoa(len(key)) + ":" + key
	}
	return strings.Join(parts, ""), false, nil
}

func uniqueIndexPartKey(table catalog.Table, part catalog.IndexPart, columnIndexes map[string]int, columns []relationColumn, row []string) (string, bool, error) {
	if part.Expression != "" {
		value, err := evaluateRelationExpression(part.Expression, columns, relationRow{values: row})
		if err != nil || value.isNull() {
			return "", value.isNull(), err
		}
		return indexPrefixKey(value.render(), part.PrefixLength), false, nil
	}
	column := columnIndexes[catalog.Key(part.Column)]
	if row[column] == storedSQLNullValue {
		return "", true, nil
	}
	return indexPrefixKey(constraintColumnKey(table, column, row[column]), part.PrefixLength), false, nil
}

func indexPrefixKey(value string, length int) string {
	if length == 0 {
		return value
	}
	runes := []rune(value)
	if length >= len(runes) {
		return value
	}
	return string(runes[:length])
}

func constraintIndexes(columns []string, indexes map[string]int) []int {
	result := make([]int, len(columns))
	for index, column := range columns {
		result[index] = indexes[catalog.Key(column)]
	}
	return result
}

func constraintRowKey(table catalog.Table, row []string, columns []int) (string, bool) {
	parts := make([]string, len(columns))
	for index, column := range columns {
		if row[column] == storedSQLNullValue {
			return "", true
		}
		parts[index] = constraintColumnKey(table, column, row[column])
	}
	return strings.Join(parts, "\x00"), false
}

func constraintColumnKey(table catalog.Table, column int, value string) string {
	typeName, known := table.ColumnType(column)
	if !known {
		return value
	}
	typ, err := parseCharacterType(typeName)
	if err != nil {
		return value
	}
	return characterComparisonKey(typ, value)
}

func validateCheckConstraint(namespaceName, tableName string, table catalog.Table, constraint catalog.Constraint) error {
	columns := relationalTableColumns(namespaceName, tableName, tableName, table)
	if _, err := evaluateRelationExpression(constraint.Check, columns, sampleRelationRow(columns)); err != nil {
		return err
	}
	for _, row := range table.Rows {
		value, err := evaluateRelationExpression(constraint.Check, columns, relationRow{values: row})
		if err != nil {
			return err
		}
		known, truth, err := truthValue(value)
		if err != nil {
			return err
		}
		if known && !truth {
			return sqlFailure{3819, "23000", "Check constraint '" + constraint.Name + "' is violated"}
		}
	}
	return nil
}

func validateForeignKeyConstraint(previous, definition catalog.Definition, namespaceName, tableName string, child catalog.Table, constraint catalog.Constraint, childIndexes map[string]int) error {
	if len(constraint.Columns) != len(constraint.ReferencedColumns) || len(constraint.ReferencedColumns) == 0 {
		return errorsConstraintDefinition("foreign key '" + constraint.Name + "' has incompatible columns")
	}
	parent, parentIndexes, err := foreignKeyParent(definition, namespaceName, constraint)
	if err != nil {
		return err
	}
	childColumns, parentColumns := constraintIndexes(constraint.Columns, childIndexes), constraintIndexes(constraint.ReferencedColumns, parentIndexes)
	return validateForeignKeyRows(previous, namespaceName, tableName, child, parent, constraint, childColumns, parentColumns)
}

func foreignKeyParent(definition catalog.Definition, namespaceName string, constraint catalog.Constraint) (catalog.Table, map[string]int, error) {
	parentNamespace := constraint.ReferencedNamespace
	if parentNamespace == "" {
		parentNamespace = namespaceName
	}
	namespace, found := definition.Namespaces[catalog.Key(parentNamespace)]
	if !found {
		return catalog.Table{}, nil, errorsConstraintDefinition("foreign key '" + constraint.Name + "' references an unknown database")
	}
	parent, found := namespace.Tables[catalog.Key(constraint.ReferencedTable)]
	if !found {
		return catalog.Table{}, nil, errorsConstraintDefinition("foreign key '" + constraint.Name + "' references an unknown table")
	}
	parentIndexes, err := tableColumnIndexes(parent)
	if err != nil {
		return catalog.Table{}, nil, err
	}
	for _, column := range constraint.ReferencedColumns {
		if _, found := parentIndexes[catalog.Key(column)]; !found {
			return catalog.Table{}, nil, errorsConstraintDefinition("foreign key '" + constraint.Name + "' references an unknown column")
		}
	}
	if !tableHasReferencedKey(parent, constraint.ReferencedColumns) {
		return catalog.Table{}, nil, errorsConstraintDefinition("foreign key '" + constraint.Name + "' requires a unique referenced key")
	}
	return parent, parentIndexes, nil
}

func validateForeignKeyRows(previous catalog.Definition, namespaceName, tableName string, child, parent catalog.Table, constraint catalog.Constraint, childColumns, parentColumns []int) error {
	parentKeys := make(map[string]struct{}, len(parent.Rows))
	for _, row := range parent.Rows {
		key, nullable := constraintRowKey(parent, row, parentColumns)
		if !nullable {
			parentKeys[key] = struct{}{}
		}
	}
	for _, row := range child.Rows {
		key, nullable := constraintRowKey(child, row, childColumns)
		if nullable {
			continue
		}
		if _, matched := parentKeys[key]; !matched {
			return foreignKeyViolation(previous, namespaceName, tableName, parent, constraint)
		}
	}
	return nil
}

func foreignKeyViolation(previous catalog.Definition, namespaceName, tableName string, parent catalog.Table, constraint catalog.Constraint) error {
	if foreignKeyParentRowsChanged(previous, namespaceName, tableName, parent, constraint) {
		return sqlFailure{1451, "23000", "Cannot delete or update a parent row: a foreign key constraint fails ('" + constraint.Name + "')"}
	}
	return sqlFailure{1452, "23000", "Cannot add or update a child row: a foreign key constraint fails ('" + constraint.Name + "')"}
}

func foreignKeyParentRowsChanged(previous catalog.Definition, namespaceName, tableName string, parent catalog.Table, constraint catalog.Constraint) bool {
	previousNamespace, found := previous.Namespaces[catalog.Key(namespaceName)]
	if !found {
		return false
	}
	previousChild, found := previousNamespace.Tables[catalog.Key(tableName)]
	if !found || !hasForeignKeyConstraint(previousChild, constraint) {
		return false
	}
	parentNamespace := constraint.ReferencedNamespace
	if parentNamespace == "" {
		parentNamespace = namespaceName
	}
	previousParentNamespace, found := previous.Namespaces[catalog.Key(parentNamespace)]
	if !found {
		return false
	}
	previousParent, found := previousParentNamespace.Tables[catalog.Key(constraint.ReferencedTable)]
	return found && !equalConstraintRows(previousParent.Rows, parent.Rows)
}

func hasForeignKeyConstraint(table catalog.Table, wanted catalog.Constraint) bool {
	for _, constraint := range table.Constraints {
		if constraint.Type == catalog.ConstraintTypeForeignKey && catalog.Key(constraint.Name) == catalog.Key(wanted.Name) {
			return true
		}
	}
	return false
}

func equalConstraintRows(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for rowIndex := range left {
		if len(left[rowIndex]) != len(right[rowIndex]) {
			return false
		}
		for columnIndex := range left[rowIndex] {
			if left[rowIndex][columnIndex] != right[rowIndex][columnIndex] {
				return false
			}
		}
	}
	return true
}

func tableHasReferencedKey(table catalog.Table, columns []string) bool {
	for _, constraint := range table.Constraints {
		if constraint.Type != catalog.ConstraintTypePrimary && constraint.Type != catalog.ConstraintTypeUnique {
			continue
		}
		if len(constraint.Columns) != len(columns) {
			continue
		}
		matches := true
		for index := range columns {
			matches = matches && catalog.Key(constraint.Columns[index]) == catalog.Key(columns[index])
		}
		if matches {
			return true
		}
	}
	return false
}
