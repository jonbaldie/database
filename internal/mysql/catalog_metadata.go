package mysql

import (
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

// catalogMetadataProjection holds the catalog facts shared by SHOW and
// information_schema for one statement snapshot.
type catalogMetadataProjection struct {
	source                   catalog.Definition
	definition               catalog.Definition
	username                 string
	allowUnconfiguredAccount bool
	namespaces               []catalogNamespaceMetadata
}

type catalogNamespaceMetadata struct {
	name   string
	tables []catalogTableMetadata
}

type catalogTableMetadata struct {
	namespace     string
	name          string
	definition    catalog.Table
	autoIncrement metadataValue
	columns       []catalogColumnMetadata
	indexes       []catalogIndexPartMetadata
	constraints   []catalogConstraintMetadata
}

type catalogColumnMetadata struct {
	name            string
	ordinalPosition int
	columnType      metadataValue
	dataType        metadataValue
	attribute       catalog.ColumnAttribute
	defaultValue    metadataValue
	extra           string
	key             string
}

type catalogIndexPartMetadata struct {
	index       catalog.Index
	part        catalog.IndexPart
	sequence    int
	nullable    bool
	cardinality int
}

type catalogConstraintMetadata struct {
	definition catalog.Constraint
}

func catalogMetadataForSession(s *session) catalogMetadataProjection {
	source := emptyDefinition()
	username := ""
	allowUnconfiguredAccount := false
	if s != nil {
		username = s.username
		allowUnconfiguredAccount = s.unconfiguredAccountPermitted()
		if s.server != nil && s.server.config.Catalog != nil {
			source = s.server.config.Catalog.Snapshot()
		}
	}
	return projectCatalogMetadata(source, username, allowUnconfiguredAccount)
}

func projectCatalogMetadata(source catalog.Definition, username string, allowUnconfiguredAccount bool) catalogMetadataProjection {
	definition := visibleCatalogDefinition(source, username)
	projection := catalogMetadataProjection{
		source:                   source,
		definition:               definition,
		username:                 username,
		allowUnconfiguredAccount: allowUnconfiguredAccount,
		namespaces:               make([]catalogNamespaceMetadata, 0, len(definition.Namespaces)),
	}
	for _, namespace := range sortedNamespaces(definition) {
		namespaceMetadata := catalogNamespaceMetadata{
			name:   namespace.Name,
			tables: make([]catalogTableMetadata, 0, len(namespace.Tables)),
		}
		for _, table := range sortedTables(namespace) {
			tableMetadata := projectCatalogTableMetadata(namespace.Name, table)
			namespaceMetadata.tables = append(namespaceMetadata.tables, tableMetadata)
		}
		projection.namespaces = append(projection.namespaces, namespaceMetadata)
	}
	return projection
}

func projectCatalogTableMetadata(namespace string, table catalog.Table) catalogTableMetadata {
	metadata := catalogTableMetadata{
		namespace:     namespace,
		name:          table.Name,
		definition:    table,
		autoIncrement: informationSchemaAutoIncrement(table),
		columns:       make([]catalogColumnMetadata, 0, len(table.Columns)),
		indexes:       make([]catalogIndexPartMetadata, 0),
		constraints:   make([]catalogConstraintMetadata, 0, len(table.Constraints)),
	}
	for index, name := range table.Columns {
		columnType := metadataValue{null: true}
		dataType := metadataValue{null: true}
		if typeName, known := table.ColumnType(index); known {
			columnType = metadataValue{value: typeName}
			dataType = metadataValue{value: baseType(typeName)}
		}
		attribute := catalog.ColumnAttributeAt(table, index)
		defaultValue := metadataValue{null: true}
		if attribute.HasDefault && attribute.Default != storedSQLNullValue {
			defaultValue = metadataValue{value: attribute.Default}
		}
		extra := ""
		if attribute.AutoIncrement {
			extra = "auto_increment"
		}
		metadata.columns = append(metadata.columns, catalogColumnMetadata{
			name:            name,
			ordinalPosition: index + 1,
			columnType:      columnType,
			dataType:        dataType,
			attribute:       attribute,
			defaultValue:    defaultValue,
			extra:           extra,
			key:             showColumnKey(table, index),
		})
	}
	for _, index := range effectiveTableIndexes(table) {
		for sequence, part := range index.Parts {
			metadata.indexes = append(metadata.indexes, catalogIndexPartMetadata{
				index:       index,
				part:        part,
				sequence:    sequence + 1,
				nullable:    columnNullable(table, part.Column),
				cardinality: len(table.Rows),
			})
		}
	}
	for _, constraint := range table.Constraints {
		metadata.constraints = append(metadata.constraints, catalogConstraintMetadata{definition: constraint})
	}
	return metadata
}

func (projection catalogMetadataProjection) namespace(name string) (catalogNamespaceMetadata, error) {
	resolution := resolveNamespace(projection.source, projection.username, name)
	if err := resolution.requireDefinition(); err != nil {
		return catalogNamespaceMetadata{}, err
	}
	for _, namespace := range projection.namespaces {
		if catalog.Key(namespace.name) == catalog.Key(name) {
			return namespace, nil
		}
	}
	return catalogNamespaceMetadata{}, sqlFailure{1049, "42000", "unknown database '" + name + "'"}
}

func (projection catalogMetadataProjection) table(namespaceName, tableName string) (catalogTableMetadata, error) {
	namespace, err := projection.namespace(namespaceName)
	if err != nil {
		return catalogTableMetadata{}, err
	}
	for _, table := range namespace.tables {
		if catalog.Key(table.name) == catalog.Key(tableName) {
			return table, nil
		}
	}
	return catalogTableMetadata{}, sqlFailure{1146, "42S02", "table '" + namespaceName + "." + tableName + "' doesn't exist"}
}

// columnPrivileges projects the account grants captured with this catalog
// snapshot into the privileges shown for each column.
func (projection catalogMetadataProjection) columnPrivileges(namespace string) string {
	var account catalog.Account
	allPrivileges := projection.username == ""
	if projection.username != "" {
		var found bool
		account, found = projection.source.Accounts[projection.username]
		if !found {
			allPrivileges = projection.allowUnconfiguredAccount
		} else if account.Locked {
			return ""
		}
	}

	privileges := []string{}
	for _, grant := range []struct {
		privilege string
		spellings []string
	}{
		{privilege: "DATA_READ", spellings: []string{"select"}},
		{privilege: "DATA_WRITE", spellings: []string{"insert", "update"}},
		{privilege: "SCHEMA_MANAGEMENT", spellings: []string{"references"}},
	} {
		if !allPrivileges && accountGrantIndex(account, grant.privilege, namespace) < 0 {
			continue
		}
		privileges = append(privileges, grant.spellings...)
	}
	return strings.Join(privileges, ",")
}

func (index catalogIndexPartMetadata) collation() string {
	if index.part.Descending {
		return "D"
	}
	return "A"
}

func (index catalogIndexPartMetadata) nonUnique() string {
	if index.index.Unique {
		return "0"
	}
	return "1"
}

func (index catalogIndexPartMetadata) showNullability() string {
	if index.nullable {
		return "YES"
	}
	return ""
}

func (index catalogIndexPartMetadata) visible() string {
	if index.index.Invisible {
		return "NO"
	}
	return "YES"
}

func (index catalogIndexPartMetadata) prefixLength() metadataValue {
	if index.part.PrefixLength == 0 {
		return metadataValue{null: true}
	}
	return metadataValue{value: strconv.Itoa(index.part.PrefixLength)}
}

func (index catalogIndexPartMetadata) informationSchemaRow(namespace, table string) []metadataValue {
	return []metadataValue{
		{value: namespace},
		{value: table},
		{value: index.nonUnique()},
		{value: index.index.Name},
		{value: strconv.Itoa(index.sequence)},
		{value: index.part.Column},
		{value: index.collation()},
		index.prefixLength(),
		{value: index.showNullability()},
		{value: "BTREE"},
		{value: ""},
		{value: index.index.Comment},
		{value: index.visible()},
		{value: index.part.Expression},
	}
}

func (index catalogIndexPartMetadata) showRow(table string) ([]string, []bool) {
	column, expression := index.part.Column, index.part.Expression
	return []string{
			table,
			index.nonUnique(),
			index.index.Name,
			strconv.Itoa(index.sequence),
			column,
			index.collation(),
			strconv.Itoa(index.cardinality),
			strconv.Itoa(index.part.PrefixLength),
			"",
			index.showNullability(),
			"BTREE",
			"",
			index.index.Comment,
			index.visible(),
			expression,
		}, []bool{
			false, false, false, false, column == "", false, false,
			index.part.PrefixLength == 0, true, false, false, false, false,
			false, expression == "",
		}
}

func (constraint catalogConstraintMetadata) typeLabel() string {
	switch constraint.definition.Type {
	case catalog.ConstraintTypePrimary:
		return "PRIMARY KEY"
	case catalog.ConstraintTypeUnique:
		return "UNIQUE"
	case catalog.ConstraintTypeForeignKey:
		return "FOREIGN KEY"
	case catalog.ConstraintTypeCheck:
		return "CHECK"
	default:
		return strings.ToUpper(constraint.definition.Type)
	}
}
