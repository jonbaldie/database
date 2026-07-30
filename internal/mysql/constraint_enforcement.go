package mysql

import (
	"fmt"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

// validateConstraintDefinition validates both the schema and every durable row
// image. mutateCatalog calls it before a transaction snapshot or catalog file
// becomes visible, so a failed write or DDL change is atomic.
func validateConstraintDefinition(definition catalog.Definition) error {
	for namespaceKey, namespace := range definition.Namespaces {
		namespaceName := namespace.Name
		if namespaceName == "" {
			namespaceName = namespaceKey
		}
		for tableKey, table := range namespace.Tables {
			tableName := table.Name
			if tableName == "" {
				tableName = tableKey
			}
			if err := validateTableConstraints(definition, namespaceName, tableName, table); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTableConstraints(definition catalog.Definition, namespaceName, tableName string, table catalog.Table) error {
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return err
	}
	if err := validateConstraintDeclarations(definition, namespaceName, tableName, table, indexes); err != nil {
		return err
	}
	return validateNotNullColumns(table, indexes)
}

func validateConstraintDeclarations(definition catalog.Definition, namespaceName, tableName string, table catalog.Table, indexes map[string]int) error {
	seen := map[string]bool{}
	for _, constraint := range table.Constraints {
		if err := validateConstraintDeclaration(seen, definition, namespaceName, tableName, table, constraint, indexes); err != nil {
			return err
		}
	}
	return nil
}

func validateConstraintDeclaration(seen map[string]bool, definition catalog.Definition, namespaceName, tableName string, table catalog.Table, constraint catalog.Constraint, indexes map[string]int) error {
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
	return validateConstraintRows(definition, namespaceName, tableName, table, constraint, indexes)
}

func validateConstraintRows(definition catalog.Definition, namespaceName, tableName string, table catalog.Table, constraint catalog.Constraint, indexes map[string]int) error {
	switch constraint.Type {
	case "primary", "unique":
		return validateUniqueConstraint(table, constraint, indexes)
	case "check":
		return validateCheckConstraint(namespaceName, tableName, table, constraint)
	case "foreign_key":
		return validateForeignKeyConstraint(definition, namespaceName, table, constraint, indexes)
	default:
		return errorsConstraintDefinition("unknown constraint type '" + constraint.Type + "'")
	}
}

func errorsConstraintDefinition(message string) error { return sqlFailure{3813, "HY000", message} }

func validateConstraintColumns(constraint catalog.Constraint, indexes map[string]int) error {
	if constraint.Type == "check" {
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
		if constraint.Type != "primary" {
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
	seen := map[string]bool{}
	for _, row := range table.Rows {
		key, nullable := constraintRowKey(table, row, columns)
		if nullable && constraint.Type == "unique" {
			continue
		}
		if seen[key] {
			return sqlFailure{1062, "23000", "Duplicate entry for key '" + constraint.Name + "'"}
		}
		seen[key] = true
	}
	return nil
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

func validateForeignKeyConstraint(definition catalog.Definition, namespaceName string, child catalog.Table, constraint catalog.Constraint, childIndexes map[string]int) error {
	if len(constraint.Columns) != len(constraint.ReferencedColumns) || len(constraint.ReferencedColumns) == 0 {
		return errorsConstraintDefinition("foreign key '" + constraint.Name + "' has incompatible columns")
	}
	parent, parentIndexes, err := foreignKeyParent(definition, namespaceName, constraint)
	if err != nil {
		return err
	}
	childColumns, parentColumns := constraintIndexes(constraint.Columns, childIndexes), constraintIndexes(constraint.ReferencedColumns, parentIndexes)
	return validateForeignKeyRows(child, parent, constraint, childColumns, parentColumns)
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

func validateForeignKeyRows(child, parent catalog.Table, constraint catalog.Constraint, childColumns, parentColumns []int) error {
	for _, row := range child.Rows {
		key, nullable := constraintRowKey(child, row, childColumns)
		if nullable {
			continue
		}
		matched := false
		for _, parentRow := range parent.Rows {
			parentKey, parentNull := constraintRowKey(parent, parentRow, parentColumns)
			if !parentNull && parentKey == key {
				matched = true
				break
			}
		}
		if !matched {
			return sqlFailure{1452, "23000", "Cannot add or update a child row: a foreign key constraint fails ('" + constraint.Name + "')"}
		}
	}
	return nil
}

func tableHasReferencedKey(table catalog.Table, columns []string) bool {
	for _, constraint := range table.Constraints {
		if constraint.Type != "primary" && constraint.Type != "unique" {
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
