package mysql

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

type ddlActionKind uint8

const (
	ddlAddColumn ddlActionKind = iota
	ddlDropColumn
	ddlRenameColumn
	ddlModifyColumn
)

type ddlAction struct {
	kind        ddlActionKind
	name        string
	newName     string
	typeName    string
	ifExists    bool
	ifNotExists bool
}

const maxTableColumns = 1024

type ddlExecutor struct{ *session }

func (s *textStatementExecutor) ddlStatement(query, lower string) (*queryResult, bool, error) {
	executor := ddlExecutor{s.session}
	switch {
	case strings.HasPrefix(lower, "drop database "), strings.HasPrefix(lower, "drop schema "):
		return nil, true, executor.dropDatabase(query)
	case strings.HasPrefix(lower, "drop table "):
		return nil, true, executor.dropTable(query)
	case strings.HasPrefix(lower, "truncate table "):
		return nil, true, executor.truncateTable(query)
	case strings.HasPrefix(lower, "rename table "):
		return nil, true, executor.renameTable(query)
	case strings.HasPrefix(lower, "alter table "):
		return nil, true, executor.alterTable(query)
	default:
		return nil, false, nil
	}
}

func (s *ddlExecutor) dropDatabase(query string) error {
	keyword := "DATABASE "
	if strings.HasPrefix(strings.ToLower(query), "drop schema ") {
		keyword = "SCHEMA "
	}
	name, ifExists, ok := parseDropObject(strings.TrimSpace(query[len("DROP ")+len(keyword):]))
	if !ok {
		return sqlFailure{1064, "42000", "malformed DROP DATABASE"}
	}
	if strings.EqualFold(name, informationSchemaName) {
		return sqlFailure{1044, "42000", "information_schema is read-only"}
	}
	key := catalog.Key(name)
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		if _, found := definition.Namespaces[key]; !found {
			if ifExists {
				return nil
			}
			return errors.New("unknown database")
		}
		delete(definition.Namespaces, key)
		return nil
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1008, "HY000", err.Error()})
	}
	if strings.EqualFold(s.database, name) {
		s.database = ""
	}
	return nil
}

func (s *ddlExecutor) dropTable(query string) error {
	target, ifExists, ok := parseDropObject(strings.TrimSpace(query[len("DROP TABLE "):]))
	if !ok {
		return sqlFailure{1064, "42000", "malformed DROP TABLE"}
	}
	namespace, name, err := ddlTableTarget(s.session, target)
	if err != nil {
		return err
	}
	key := catalog.Key(name)
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		namespaceDefinition, found := definition.Namespaces[catalog.Key(namespace)]
		if !found {
			return errors.New("namespace does not exist")
		}
		if _, found := namespaceDefinition.Tables[key]; !found {
			if ifExists {
				return nil
			}
			return errors.New("table does not exist")
		}
		delete(namespaceDefinition.Tables, key)
		definition.Namespaces[catalog.Key(namespace)] = namespaceDefinition
		return nil
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1051, "42S02", err.Error()})
	}
	return nil
}

func (s *ddlExecutor) truncateTable(query string) error {
	target := strings.TrimSpace(query[len("TRUNCATE TABLE "):])
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return sqlFailure{1064, "42000", "malformed TRUNCATE TABLE"}
	}
	namespace, name, err := ddlTableTarget(s.session, target)
	if err != nil {
		return err
	}
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		namespaceDefinition, found := definition.Namespaces[catalog.Key(namespace)]
		if !found {
			return errors.New("namespace does not exist")
		}
		table, found := namespaceDefinition.Tables[catalog.Key(name)]
		if !found {
			return errors.New("table does not exist")
		}
		table.Rows = nil
		namespaceDefinition.Tables[catalog.Key(name)] = table
		definition.Namespaces[catalog.Key(namespace)] = namespaceDefinition
		return nil
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1146, "42S02", err.Error()})
	}
	return nil
}

func (s *ddlExecutor) renameTable(query string) error {
	left, right, ok := splitDDLKeyword(strings.TrimSpace(query[len("RENAME TABLE "):]), "to")
	if !ok {
		return sqlFailure{1064, "42000", "malformed RENAME TABLE"}
	}
	fromNamespace, fromName, err := ddlTableTarget(s.session, left)
	if err != nil {
		return err
	}
	toParts, valid := splitQualifiedIdentifier(right)
	if !valid || len(toParts) == 0 || len(toParts) > 2 {
		return sqlFailure{1064, "42000", "invalid RENAME TABLE target"}
	}
	toNamespace, toName, err := ddlTableTarget(s.session, right)
	if err != nil {
		return err
	}
	if !strings.EqualFold(fromNamespace, toNamespace) {
		return sqlFailure{1146, "42S02", "cross-database table rename is unsupported"}
	}
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		return renameTableInDefinition(definition, fromNamespace, fromName, toName)
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1050, "42S01", err.Error()})
	}
	return nil
}

func renameTableInDefinition(definition *catalog.Definition, namespaceName, oldName, newName string) error {
	namespace := definition.Namespaces[catalog.Key(namespaceName)]
	table, found := namespace.Tables[catalog.Key(oldName)]
	if !found {
		return errors.New("table does not exist")
	}
	if _, found := namespace.Tables[catalog.Key(newName)]; found {
		return errors.New("table already exists")
	}
	delete(namespace.Tables, catalog.Key(oldName))
	table.Name = newName
	namespace.Tables[catalog.Key(newName)] = table
	definition.Namespaces[catalog.Key(namespaceName)] = namespace
	return nil
}

func (s *ddlExecutor) alterTable(query string) error {
	rest := strings.TrimSpace(query[len("ALTER TABLE "):])
	target, actionsText, ok := splitDDLTargetAndRest(rest)
	if !ok {
		return sqlFailure{1064, "42000", "malformed ALTER TABLE"}
	}
	actions, err := parseAlterTableActions(actionsText)
	if err != nil {
		return err
	}
	namespace, name, err := ddlTableTarget(s.session, target)
	if err != nil {
		return err
	}
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		namespaceDefinition, found := definition.Namespaces[catalog.Key(namespace)]
		if !found {
			return errors.New("namespace does not exist")
		}
		table, found := namespaceDefinition.Tables[catalog.Key(name)]
		if !found {
			return errors.New("table does not exist")
		}
		updated, err := applyTableDefinitionActions(table, actions)
		if err != nil {
			return err
		}
		namespaceDefinition.Tables[catalog.Key(name)] = updated
		definition.Namespaces[catalog.Key(namespace)] = namespaceDefinition
		return nil
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1025, "HY000", err.Error()})
	}
	return nil
}

func parseDropObject(value string) (string, bool, bool) {
	value = strings.TrimSpace(value)
	ifExists := false
	if strings.HasPrefix(strings.ToLower(value), "if exists ") {
		ifExists = true
		value = strings.TrimSpace(value[len("IF EXISTS "):])
	}
	name, ok := singleIdentifier(value)
	return name, ifExists, ok
}

func splitDDLKeyword(value, keyword string) (string, string, bool) {
	index := keywordAt(value, keyword)
	if index < 0 {
		return "", "", false
	}
	left, right := strings.TrimSpace(value[:index]), strings.TrimSpace(value[index+len(keyword):])
	return left, right, left != "" && right != ""
}

func splitDDLTargetAndRest(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	first, remainder, ok := leadingIdentifierToken(value)
	if !ok {
		return "", "", false
	}
	target := first
	for {
		remainder = strings.TrimSpace(remainder)
		if !strings.HasPrefix(remainder, ".") {
			return target, remainder, remainder != ""
		}
		part, after, ok := leadingIdentifierToken(strings.TrimSpace(remainder[1:]))
		if !ok {
			return "", "", false
		}
		target += "." + part
		remainder = after
	}
}

func ddlTableTarget(s *session, target string) (string, string, error) {
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return "", "", sqlFailure{1064, "42000", "invalid table name"}
	}
	for _, part := range parts {
		if err := validateIdentifierLength(part); err != nil {
			return "", "", err
		}
	}
	return tableTarget(&relationExecutor{s}, parts)
}

func parseAlterTableActions(value string) ([]ddlAction, error) {
	parts := splitCSV(value)
	actions := make([]ddlAction, 0, len(parts))
	for _, part := range parts {
		action, err := parseAlterTableAction(part)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if len(actions) == 0 {
		return nil, sqlFailure{1064, "42000", "ALTER TABLE requires an action"}
	}
	return actions, nil
}

func parseAlterTableAction(value string) (ddlAction, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "add "):
		return parseAddColumnAction(strings.TrimSpace(value[len("ADD "):]))
	case strings.HasPrefix(lower, "drop "):
		return parseDropColumnAction(strings.TrimSpace(value[len("DROP "):]))
	case strings.HasPrefix(lower, "rename "):
		return parseRenameColumnAction(strings.TrimSpace(value[len("RENAME "):]))
	case strings.HasPrefix(lower, "change "):
		return parseChangeColumnAction(strings.TrimSpace(value[len("CHANGE "):]))
	case strings.HasPrefix(lower, "modify "):
		return parseModifyColumnAction(strings.TrimSpace(value[len("MODIFY "):]))
	default:
		return ddlAction{}, sqlFailure{1235, "42000", "unsupported ALTER TABLE action"}
	}
}

func parseAddColumnAction(value string) (ddlAction, error) {
	value = stripOptionalKeyword(value, "column")
	ifNotExists := false
	if strings.HasPrefix(strings.ToLower(value), "if not exists ") {
		ifNotExists = true
		value = strings.TrimSpace(value[len("IF NOT EXISTS "):])
	}
	column, typeName, err := parseAlterColumnDefinition(value)
	if err != nil {
		return ddlAction{}, err
	}
	return ddlAction{kind: ddlAddColumn, name: column, typeName: typeName, ifNotExists: ifNotExists}, nil
}

func parseDropColumnAction(value string) (ddlAction, error) {
	value = stripOptionalKeyword(value, "column")
	ifExists := false
	if strings.HasPrefix(strings.ToLower(value), "if exists ") {
		ifExists = true
		value = strings.TrimSpace(value[len("IF EXISTS "):])
	}
	name, ok := singleIdentifier(value)
	if !ok {
		return ddlAction{}, sqlFailure{1064, "42000", "invalid column name"}
	}
	return ddlAction{kind: ddlDropColumn, name: name, ifExists: ifExists}, nil
}

func parseRenameColumnAction(value string) (ddlAction, error) {
	value = stripOptionalKeyword(value, "column")
	oldName, newName, ok := splitDDLKeyword(value, "to")
	if !ok {
		return ddlAction{}, sqlFailure{1064, "42000", "malformed RENAME COLUMN"}
	}
	oldName, oldOK := singleIdentifier(oldName)
	newName, newOK := singleIdentifier(newName)
	if !oldOK || !newOK {
		return ddlAction{}, sqlFailure{1064, "42000", "invalid column name"}
	}
	if err := validateIdentifierLength(newName); err != nil {
		return ddlAction{}, err
	}
	return ddlAction{kind: ddlRenameColumn, name: oldName, newName: newName}, nil
}

func parseChangeColumnAction(value string) (ddlAction, error) {
	value = stripOptionalKeyword(value, "column")
	oldName, remainder, ok := consumeIdentifier(value)
	if !ok {
		return ddlAction{}, sqlFailure{1064, "42000", "invalid column name"}
	}
	newName, typeName, err := parseAlterColumnDefinition(remainder)
	if err != nil {
		return ddlAction{}, err
	}
	return ddlAction{kind: ddlModifyColumn, name: oldName, newName: newName, typeName: typeName}, nil
}

func parseModifyColumnAction(value string) (ddlAction, error) {
	value = stripOptionalKeyword(value, "column")
	name, typeName, err := parseAlterColumnDefinition(value)
	if err != nil {
		return ddlAction{}, err
	}
	return ddlAction{kind: ddlModifyColumn, name: name, typeName: typeName}, nil
}

func stripOptionalKeyword(value, keyword string) string {
	prefix := keyword + " "
	if strings.HasPrefix(strings.ToLower(value), prefix) {
		return strings.TrimSpace(value[len(prefix):])
	}
	return strings.TrimSpace(value)
}

func parseAlterColumnDefinition(value string) (string, string, error) {
	column, typeName, err := parseTableColumn(value)
	if err != nil {
		return "", "", err
	}
	if typeName == "" {
		return "", "", sqlFailure{1064, "42000", "column type is required"}
	}
	return column, typeName, nil
}

func applyTableDefinitionActions(table catalog.Table, actions []ddlAction) (catalog.Table, error) {
	updated := cloneCatalogTable(table)
	for _, action := range actions {
		if err := applyTableDefinitionAction(&updated, action); err != nil {
			return catalog.Table{}, err
		}
	}
	return updated, nil
}

func applyTableDefinitionAction(table *catalog.Table, action ddlAction) error {
	switch action.kind {
	case ddlAddColumn:
		return addTableColumn(table, action)
	case ddlDropColumn:
		return dropTableColumn(table, action)
	case ddlRenameColumn:
		return renameTableColumn(table, action)
	case ddlModifyColumn:
		return modifyTableColumn(table, action)
	default:
		return errors.New("unsupported DDL action")
	}
}

func addTableColumn(table *catalog.Table, action ddlAction) error {
	if tableColumnIndex(table.Columns, action.name) >= 0 {
		if action.ifNotExists {
			return nil
		}
		return errors.New("column already exists")
	}
	if len(table.Columns) >= maxTableColumns {
		return sqlFailure{1117, "HY000", "too many columns"}
	}
	ensureColumnTypes(table)
	table.Columns = append(table.Columns, action.name)
	table.ColumnTypes = append(table.ColumnTypes, action.typeName)
	for rowIndex := range table.Rows {
		table.Rows[rowIndex] = append(table.Rows[rowIndex], storedSQLNullValue)
	}
	return nil
}

func dropTableColumn(table *catalog.Table, action ddlAction) error {
	index := tableColumnIndex(table.Columns, action.name)
	if index < 0 {
		if action.ifExists {
			return nil
		}
		return errors.New("unknown column")
	}
	if len(table.Columns) == 1 {
		return errors.New("a table must retain one column")
	}
	removeTableColumn(table, index)
	return nil
}

func renameTableColumn(table *catalog.Table, action ddlAction) error {
	index := tableColumnIndex(table.Columns, action.name)
	if index < 0 {
		return errors.New("unknown column")
	}
	if other := tableColumnIndex(table.Columns, action.newName); other >= 0 && other != index {
		return errors.New("column already exists")
	}
	table.Columns[index] = action.newName
	return nil
}

func modifyTableColumn(table *catalog.Table, action ddlAction) error {
	index := tableColumnIndex(table.Columns, action.name)
	if index < 0 {
		return errors.New("unknown column")
	}
	if action.newName != "" && tableColumnIndex(table.Columns, action.newName) >= 0 && action.newName != action.name {
		return errors.New("column already exists")
	}
	if err := convertTableColumn(table, index, action.typeName); err != nil {
		return err
	}
	if action.newName != "" {
		table.Columns[index] = action.newName
	}
	return nil
}

func convertTableColumn(table *catalog.Table, index int, typeName string) error {
	ensureColumnTypes(table)
	for rowIndex, row := range table.Rows {
		if row[index] == storedSQLNullValue {
			continue
		}
		value, err := canonicalColumnValueAtOffset(catalog.Table{Columns: []string{table.Columns[index]}, ColumnTypes: []string{typeName}}, 0, row[index], rowIndex+1, 0)
		if err != nil {
			return fmt.Errorf("cannot convert column %q: %w", table.Columns[index], err)
		}
		row[index] = value
	}
	table.ColumnTypes[index] = typeName
	return nil
}

func cloneCatalogTable(table catalog.Table) catalog.Table {
	return catalog.Table{
		Name:        table.Name,
		Columns:     append([]string(nil), table.Columns...),
		ColumnTypes: append([]string(nil), table.ColumnTypes...),
		Rows:        cloneRows(table.Rows),
	}
}

func ensureColumnTypes(table *catalog.Table) {
	if len(table.ColumnTypes) == 0 {
		table.ColumnTypes = make([]string, len(table.Columns))
	}
}

func tableColumnIndex(columns []string, name string) int {
	key := catalog.Key(name)
	for index, column := range columns {
		if catalog.Key(column) == key {
			return index
		}
	}
	return -1
}

func removeTableColumn(table *catalog.Table, index int) {
	table.Columns = append(table.Columns[:index], table.Columns[index+1:]...)
	if len(table.ColumnTypes) > index {
		table.ColumnTypes = append(table.ColumnTypes[:index], table.ColumnTypes[index+1:]...)
	}
	for rowIndex, row := range table.Rows {
		table.Rows[rowIndex] = append(row[:index], row[index+1:]...)
	}
}
