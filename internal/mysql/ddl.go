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
	ddlAddConstraint
	ddlAddIndex
	ddlDropIndex
	ddlAlterIndex
)

type ddlAction struct {
	kind        ddlActionKind
	name        string
	newName     string
	typeName    string
	attribute   catalog.ColumnAttribute
	ifExists    bool
	ifNotExists bool
	constraint  catalog.Constraint
	index       catalog.Index
}

const maxTableColumns = 1024

type ddlExecutor struct{ *session }

func (s *textStatementExecutor) ddlStatement(query, lower string) (*queryResult, bool, error) {
	executor := ddlExecutor{s.session}
	switch {
	case strings.HasPrefix(lower, "create unique index "), strings.HasPrefix(lower, "create index "):
		return nil, true, executor.createIndex(query)
	case strings.HasPrefix(lower, "drop index "):
		return nil, true, executor.dropIndex(query)
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

func (s *ddlExecutor) createIndex(query string) error {
	target, index, err := parseCreateIndex(query)
	if err != nil {
		return err
	}
	namespace, tableName, err := ddlTableTarget(s.session, target)
	if err != nil {
		return err
	}
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		return addIndexToDefinition(definition, namespace, tableName, index)
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1061, "42000", err.Error()})
	}
	return nil
}

func parseCreateIndex(query string) (string, catalog.Index, error) {
	definition := strings.TrimSpace(query[len("CREATE "):])
	left, right, found := splitDDLKeyword(definition, "on")
	if !found {
		return "", catalog.Index{}, sqlFailure{1064, "42000", "malformed CREATE INDEX"}
	}
	open := strings.Index(right, "(")
	if open < 1 {
		return "", catalog.Index{}, sqlFailure{1064, "42000", "CREATE INDEX requires a table and key parts"}
	}
	target := strings.TrimSpace(right[:open])
	head, method, err := parseCreateIndexHead(left)
	if err != nil {
		return "", catalog.Index{}, err
	}
	index, err := parseTableIndexDefinition(head + " " + right[open:] + method)
	if err != nil {
		return "", catalog.Index{}, err
	}
	if index.Name == "" {
		return "", catalog.Index{}, sqlFailure{1064, "42000", "CREATE INDEX requires an index name"}
	}
	return target, index, nil
}

func parseCreateIndexHead(value string) (string, string, error) {
	left, right, found := splitDDLKeyword(value, "using")
	if !found {
		return value, "", nil
	}
	method, valid := singleIdentifier(right)
	if !valid || !strings.EqualFold(method, "btree") {
		return "", "", sqlFailure{1235, "42000", "only BTREE indexes are supported"}
	}
	return left, " USING BTREE", nil
}

func (s *ddlExecutor) dropIndex(query string) error {
	name, target, err := parseDropIndex(query)
	if err != nil {
		return err
	}
	namespace, tableName, err := ddlTableTarget(s.session, target)
	if err != nil {
		return err
	}
	if err := s.mutateCatalog(func(definition *catalog.Definition) error {
		return dropIndexFromDefinition(definition, namespace, tableName, name)
	}); err != nil {
		return catalogMutationFailure(err, sqlFailure{1091, "42000", err.Error()})
	}
	return nil
}

func parseDropIndex(query string) (string, string, error) {
	value := strings.TrimSpace(query[len("DROP INDEX "):])
	name, target, found := splitDDLKeyword(value, "on")
	if !found {
		return "", "", sqlFailure{1064, "42000", "malformed DROP INDEX"}
	}
	name, valid := singleIdentifier(name)
	if !valid {
		return "", "", sqlFailure{1064, "42000", "invalid index name"}
	}
	return name, target, nil
}

func addIndexToDefinition(definition *catalog.Definition, namespaceName, tableName string, index catalog.Index) error {
	namespace, found := definition.Namespaces[catalog.Key(namespaceName)]
	if !found {
		return errors.New("namespace does not exist")
	}
	table, found := namespace.Tables[catalog.Key(tableName)]
	if !found {
		return errors.New("table does not exist")
	}
	indexes, err := namedTableIndexes(table.Name, append(catalog.CloneIndexes(table.Indexes), index), table.Constraints)
	if err != nil {
		return err
	}
	table.Indexes = indexes
	namespace.Tables[catalog.Key(tableName)] = table
	definition.Namespaces[catalog.Key(namespaceName)] = namespace
	return nil
}

func dropIndexFromDefinition(definition *catalog.Definition, namespaceName, tableName, name string) error {
	namespace, found := definition.Namespaces[catalog.Key(namespaceName)]
	if !found {
		return errors.New("namespace does not exist")
	}
	table, found := namespace.Tables[catalog.Key(tableName)]
	if !found {
		return errors.New("table does not exist")
	}
	indexes, found := withoutTableIndex(table.Indexes, name)
	if !found {
		return errors.New("can't drop index; check that it exists")
	}
	table.Indexes = indexes
	namespace.Tables[catalog.Key(tableName)] = table
	definition.Namespaces[catalog.Key(namespaceName)] = namespace
	return nil
}

func withoutTableIndex(indexes []catalog.Index, name string) ([]catalog.Index, bool) {
	for number, index := range indexes {
		if catalog.Key(index.Name) != catalog.Key(name) {
			continue
		}
		return append(indexes[:number:number], indexes[number+1:]...), true
	}
	return indexes, false
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
		removeNamespaceGrants(definition, name)
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
	return tableTarget(&relationExecutor{session: s}, parts)
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
		return parseAddTableAction(strings.TrimSpace(value[len("ADD "):]))
	case strings.HasPrefix(lower, "drop "):
		return parseDropTableAction(strings.TrimSpace(value[len("DROP "):]))
	case strings.HasPrefix(lower, "alter index "):
		return parseAlterIndexAction(strings.TrimSpace(value[len("ALTER INDEX "):]))
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

func parseAddTableAction(value string) (ddlAction, error) {
	if isTableIndexDefinition(value) {
		index, err := parseTableIndexDefinition(value)
		if err != nil {
			return ddlAction{}, err
		}
		return ddlAction{kind: ddlAddIndex, index: index}, nil
	}
	if isTableConstraintDefinition(value) {
		constraint, err := parseTableConstraint(value)
		if err != nil {
			return ddlAction{}, err
		}
		return ddlAction{kind: ddlAddConstraint, constraint: constraint}, nil
	}
	return parseAddColumnAction(value)
}

func parseDropTableAction(value string) (ddlAction, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "index ") || strings.HasPrefix(lower, "key ") {
		name, valid := singleIdentifier(strings.TrimSpace(value[strings.Index(value, " ")+1:]))
		if !valid {
			return ddlAction{}, sqlFailure{1064, "42000", "invalid index name"}
		}
		return ddlAction{kind: ddlDropIndex, name: name}, nil
	}
	return parseDropColumnAction(value)
}

func parseAlterIndexAction(value string) (ddlAction, error) {
	name, remainder, valid := consumeIdentifier(value)
	if !valid {
		return ddlAction{}, sqlFailure{1064, "42000", "invalid index name"}
	}
	visibility := strings.ToLower(strings.TrimSpace(remainder))
	if visibility != "visible" && visibility != "invisible" {
		return ddlAction{}, sqlFailure{1064, "42000", "invalid index visibility"}
	}
	return ddlAction{kind: ddlAlterIndex, name: name, index: catalog.Index{Invisible: visibility == "invisible"}}, nil
}

func parseAddColumnAction(value string) (ddlAction, error) {
	value = stripOptionalKeyword(value, "column")
	ifNotExists := false
	if strings.HasPrefix(strings.ToLower(value), "if not exists ") {
		ifNotExists = true
		value = strings.TrimSpace(value[len("IF NOT EXISTS "):])
	}
	column, typeName, attribute, err := parseAlterColumnDefinition(value)
	if err != nil {
		return ddlAction{}, err
	}
	return ddlAction{kind: ddlAddColumn, name: column, typeName: typeName, attribute: attribute, ifNotExists: ifNotExists}, nil
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
	newName, typeName, attribute, err := parseAlterColumnDefinition(remainder)
	if err != nil {
		return ddlAction{}, err
	}
	return ddlAction{kind: ddlModifyColumn, name: oldName, newName: newName, typeName: typeName, attribute: attribute}, nil
}

func parseModifyColumnAction(value string) (ddlAction, error) {
	value = stripOptionalKeyword(value, "column")
	name, typeName, attribute, err := parseAlterColumnDefinition(value)
	if err != nil {
		return ddlAction{}, err
	}
	return ddlAction{kind: ddlModifyColumn, name: name, typeName: typeName, attribute: attribute}, nil
}

func stripOptionalKeyword(value, keyword string) string {
	prefix := keyword + " "
	if strings.HasPrefix(strings.ToLower(value), prefix) {
		return strings.TrimSpace(value[len(prefix):])
	}
	return strings.TrimSpace(value)
}

func parseAlterColumnDefinition(value string) (string, string, catalog.ColumnAttribute, error) {
	column, typeName, attribute, constraints, err := parseTableColumn(value)
	if err != nil {
		return "", "", catalog.ColumnAttribute{}, err
	}
	if len(constraints) != 0 {
		return "", "", catalog.ColumnAttribute{}, sqlFailure{1235, "42000", "column constraints require ADD CONSTRAINT"}
	}
	if typeName == "" {
		return "", "", catalog.ColumnAttribute{}, sqlFailure{1064, "42000", "column type is required"}
	}
	return column, typeName, attribute, nil
}

func applyTableDefinitionActions(table catalog.Table, actions []ddlAction) (catalog.Table, error) {
	updated := cloneCatalogTable(table)
	for _, action := range actions {
		if err := applyTableDefinitionAction(&updated, action); err != nil {
			return catalog.Table{}, err
		}
	}
	if err := validateTableIndexes(updated); err != nil {
		return catalog.Table{}, err
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
	case ddlAddConstraint:
		return addTableConstraint(table, action.constraint)
	case ddlAddIndex:
		return addTableIndex(table, action.index)
	case ddlDropIndex:
		return dropTableIndex(table, action.name)
	case ddlAlterIndex:
		return alterTableIndexVisibility(table, action.name, action.index.Invisible)
	default:
		return errors.New("unsupported DDL action")
	}
}

func addTableIndex(table *catalog.Table, index catalog.Index) error {
	indexes, err := namedTableIndexes(table.Name, append(catalog.CloneIndexes(table.Indexes), index), table.Constraints)
	if err != nil {
		return err
	}
	table.Indexes = indexes
	return validateTableIndexes(*table)
}

func dropTableIndex(table *catalog.Table, name string) error {
	indexes, found := withoutTableIndex(table.Indexes, name)
	if !found {
		return errors.New("can't drop index; check that it exists")
	}
	table.Indexes = indexes
	return nil
}

func alterTableIndexVisibility(table *catalog.Table, name string, invisible bool) error {
	for number := range table.Indexes {
		if catalog.Key(table.Indexes[number].Name) != catalog.Key(name) {
			continue
		}
		table.Indexes[number].Invisible = invisible
		return nil
	}
	if catalog.Key(name) == catalog.Key("PRIMARY") {
		if invisible {
			return sqlFailure{3522, "HY000", "a primary key index cannot be invisible"}
		}
		return errors.New("can't alter primary index visibility")
	}
	return errors.New("can't alter index; check that it exists")
}

func addTableConstraint(table *catalog.Table, constraint catalog.Constraint) error {
	candidates := append(catalog.CloneConstraints(table.Constraints), constraint)
	named, err := namedTableConstraints(table.Name, candidates)
	if err != nil {
		return err
	}
	constraint = named[len(named)-1]
	for _, existing := range table.Constraints {
		if catalog.Key(existing.Name) == catalog.Key(constraint.Name) {
			return errors.New("constraint already exists")
		}
	}
	table.Constraints = append(table.Constraints, constraint)
	return applyPrimaryColumnRules(table)
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
	ensureColumnAttributes(table)
	table.Columns = append(table.Columns, action.name)
	table.ColumnTypes = append(table.ColumnTypes, action.typeName)
	table.ColumnAttributes = append(table.ColumnAttributes, action.attribute)
	if action.attribute.HasDefault {
		canonical, err := canonicalColumnValue(*table, len(table.Columns)-1, action.attribute.Default, 1)
		if err != nil {
			return err
		}
		table.ColumnAttributes[len(table.ColumnAttributes)-1].Default = canonical
	}
	for rowIndex := range table.Rows {
		value := storedSQLNullValue
		if table.ColumnAttributes[len(table.ColumnAttributes)-1].HasDefault {
			value = table.ColumnAttributes[len(table.ColumnAttributes)-1].Default
		}
		table.Rows[rowIndex] = append(table.Rows[rowIndex], value)
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
	renameTableIndexColumns(table, action.name, action.newName)
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
		renameTableIndexColumns(table, action.name, action.newName)
	}
	ensureColumnAttributes(table)
	if action.attribute.HasDefault {
		canonical, err := canonicalColumnValue(*table, index, action.attribute.Default, 1)
		if err != nil {
			return err
		}
		action.attribute.Default = canonical
	}
	table.ColumnAttributes[index] = action.attribute
	return nil
}

func renameTableIndexColumns(table *catalog.Table, oldName, newName string) {
	for index := range table.Indexes {
		for part := range table.Indexes[index].Parts {
			if catalog.Key(table.Indexes[index].Parts[part].Column) == catalog.Key(oldName) {
				table.Indexes[index].Parts[part].Column = newName
			}
		}
	}
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
		Name:             table.Name,
		Columns:          append([]string(nil), table.Columns...),
		ColumnTypes:      append([]string(nil), table.ColumnTypes...),
		ColumnAttributes: append([]catalog.ColumnAttribute(nil), table.ColumnAttributes...),
		Constraints:      catalog.CloneConstraints(table.Constraints),
		Indexes:          catalog.CloneIndexes(table.Indexes),
		Rows:             cloneRows(table.Rows),
	}
}

func ensureColumnTypes(table *catalog.Table) {
	if len(table.ColumnTypes) == 0 {
		table.ColumnTypes = make([]string, len(table.Columns))
	}
}

func ensureColumnAttributes(table *catalog.Table) {
	if len(table.ColumnAttributes) == 0 {
		table.ColumnAttributes = make([]catalog.ColumnAttribute, len(table.Columns))
		for index := range table.ColumnAttributes {
			table.ColumnAttributes[index].Nullable = true
		}
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
	if len(table.ColumnAttributes) > index {
		table.ColumnAttributes = append(table.ColumnAttributes[:index], table.ColumnAttributes[index+1:]...)
	}
	for rowIndex, row := range table.Rows {
		table.Rows[rowIndex] = append(row[:index], row[index+1:]...)
	}
}
