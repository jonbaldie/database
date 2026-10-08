package mysql

import (
	"sort"
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

// informationSchemaCatalogName is the MySQL catalog-name field value.
const informationSchemaCatalogName = "def"

// foreignKeyRule is the referential action for every v0.1 foreign key. v0.1
// has no ON UPDATE or ON DELETE clauses and checks each statement at once.
const foreignKeyRule = "NO ACTION"

type informationSchemaRowBuilder func(*session, catalog.Definition) [][]metadataValue

var informationSchemaRowBuilders = map[string]informationSchemaRowBuilder{
	"schemata": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaSchemataRows(definition)
	},
	"tables": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaTableRows(definition)
	},
	"columns": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaColumnRows(definition)
	},
	"statistics": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaStatisticsRows(definition)
	},
	"table_constraints": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaTableConstraintRows(definition)
	},
	"key_column_usage": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaKeyColumnUsageRows(definition)
	},
	"referential_constraints": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaReferentialRows(definition)
	},
	"check_constraints": func(_ *session, definition catalog.Definition) [][]metadataValue {
		return informationSchemaCheckRows(definition)
	},
	"character_sets": func(*session, catalog.Definition) [][]metadataValue { return informationSchemaCharacterSetRows() },
	"collations":     func(*session, catalog.Definition) [][]metadataValue { return informationSchemaCollationRows() },
	"accounts":       informationSchemaAccountRows,
	"account_grants": informationSchemaAccountGrantRows,
	"processlist":    func(s *session, _ catalog.Definition) [][]metadataValue { return informationSchemaProcessListRows(s) },
}

func informationSchemaStatisticsRows(definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range sortedNamespaces(definition) {
		for _, table := range sortedTables(namespace) {
			rows = append(rows, informationSchemaTableStatistics(namespace.Name, table)...)
		}
	}
	return rows
}

func informationSchemaTableStatistics(namespace string, table catalog.Table) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, index := range effectiveTableIndexes(table) {
		nonUnique := "1"
		if index.Unique {
			nonUnique = "0"
		}
		visible := "YES"
		if index.Invisible {
			visible = "NO"
		}
		for number, part := range index.Parts {
			column := metadataValue{value: part.Column, null: part.Column == ""}
			expression := metadataValue{value: part.Expression, null: part.Expression == ""}
			collation := "A"
			if part.Descending {
				collation = "D"
			}
			subPart := metadataValue{null: true}
			if part.PrefixLength > 0 {
				subPart = metadataValue{value: strconv.Itoa(part.PrefixLength)}
			}
			nullable := ""
			if columnNullable(table, part.Column) {
				nullable = "YES"
			}
			rows = append(rows, []metadataValue{
				{value: informationSchemaCatalogName},
				{value: namespace},
				{value: table.Name},
				{value: nonUnique},
				{value: namespace},
				{value: index.Name},
				{value: strconv.Itoa(number + 1)},
				column,
				{value: collation},
				{value: strconv.Itoa(len(table.Rows))},
				subPart,
				{null: true},
				{value: nullable},
				{value: "BTREE"},
				{value: ""},
				{value: index.Comment},
				{value: visible},
				expression,
			})
		}
	}
	return rows
}

func columnNullable(table catalog.Table, name string) bool {
	if name == "" {
		return true
	}
	for index, column := range table.Columns {
		if !catalog.SameIdentifier(column, name) {
			continue
		}
		if index < len(table.ColumnAttributes) {
			return table.ColumnAttributes[index].Nullable
		}
		return true
	}
	return true
}

func informationSchemaTableConstraintRows(definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range sortedNamespaces(definition) {
		for _, table := range sortedTables(namespace) {
			for _, constraint := range table.Constraints {
				rows = append(rows, []metadataValue{
					{value: informationSchemaCatalogName},
					{value: namespace.Name},
					{value: constraint.Name},
					{value: namespace.Name},
					{value: table.Name},
					{value: constraintTypeLabel(constraint.Type)},
					{value: "YES"},
				})
			}
		}
	}
	return rows
}

func constraintTypeLabel(kind string) string {
	switch kind {
	case catalog.ConstraintTypePrimary:
		return "PRIMARY KEY"
	case catalog.ConstraintTypeUnique:
		return "UNIQUE"
	case catalog.ConstraintTypeForeignKey:
		return "FOREIGN KEY"
	case catalog.ConstraintTypeCheck:
		return "CHECK"
	default:
		return strings.ToUpper(kind)
	}
}

func informationSchemaKeyColumnUsageRows(definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range sortedNamespaces(definition) {
		for _, table := range sortedTables(namespace) {
			for _, constraint := range table.Constraints {
				rows = append(rows, keyColumnUsageRows(namespace.Name, table.Name, constraint)...)
			}
		}
	}
	return rows
}

func keyColumnUsageRows(namespace, table string, constraint catalog.Constraint) [][]metadataValue {
	if constraint.Type == catalog.ConstraintTypeCheck {
		return nil
	}
	rows := make([][]metadataValue, 0, len(constraint.Columns))
	for index, column := range constraint.Columns {
		referencedSchema, referencedTable, referencedColumn := metadataValue{null: true}, metadataValue{null: true}, metadataValue{null: true}
		uniquePosition := metadataValue{null: true}
		if constraint.Type == catalog.ConstraintTypeForeignKey {
			uniquePosition = metadataValue{value: strconv.Itoa(index + 1)}
			referencedSchema = metadataValue{value: constraint.ReferencedNamespace}
			if referencedSchema.value == "" {
				referencedSchema.value = namespace
			}
			referencedTable = metadataValue{value: constraint.ReferencedTable}
			if index < len(constraint.ReferencedColumns) {
				referencedColumn = metadataValue{value: constraint.ReferencedColumns[index]}
			}
		}
		rows = append(rows, []metadataValue{
			{value: informationSchemaCatalogName},
			{value: namespace},
			{value: constraint.Name},
			{value: informationSchemaCatalogName},
			{value: namespace},
			{value: table},
			{value: column},
			{value: strconv.Itoa(index + 1)},
			uniquePosition,
			referencedSchema,
			referencedTable,
			referencedColumn,
		})
	}
	return rows
}

func informationSchemaReferentialRows(definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range sortedNamespaces(definition) {
		for _, table := range sortedTables(namespace) {
			for _, constraint := range table.Constraints {
				if constraint.Type != catalog.ConstraintTypeForeignKey {
					continue
				}
				referencedNamespace := constraint.ReferencedNamespace
				if referencedNamespace == "" {
					referencedNamespace = namespace.Name
				}
				rows = append(rows, []metadataValue{
					{value: informationSchemaCatalogName},
					{value: namespace.Name},
					{value: constraint.Name},
					{value: informationSchemaCatalogName},
					{value: referencedNamespace},
					referencedUniqueConstraintName(definition, referencedNamespace, constraint),
					{value: "NONE"},
					{value: foreignKeyRule},
					{value: foreignKeyRule},
					{value: table.Name},
					{value: constraint.ReferencedTable},
				})
			}
		}
	}
	return rows
}

// referencedUniqueConstraintName names the parent unique key whose columns
// match the foreign key's referenced columns. It is NULL when the parent table
// is not visible or no such key exists.
func referencedUniqueConstraintName(definition catalog.Definition, namespaceName string, constraint catalog.Constraint) metadataValue {
	for _, namespace := range definition.Namespaces {
		if !catalog.SameIdentifier(namespace.Name, namespaceName) {
			continue
		}
		for _, table := range namespace.Tables {
			if !catalog.SameIdentifier(table.Name, constraint.ReferencedTable) {
				continue
			}
			for _, index := range effectiveTableIndexes(table) {
				if index.Unique && indexCoversColumns(index, constraint.ReferencedColumns) {
					return metadataValue{value: index.Name}
				}
			}
		}
	}
	return metadataValue{null: true}
}

func indexCoversColumns(index catalog.Index, columns []string) bool {
	if len(index.Parts) != len(columns) {
		return false
	}
	for number, part := range index.Parts {
		if part.Column == "" || part.PrefixLength > 0 || !catalog.SameIdentifier(part.Column, columns[number]) {
			return false
		}
	}
	return true
}

func informationSchemaCheckRows(definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range sortedNamespaces(definition) {
		for _, table := range sortedTables(namespace) {
			for _, constraint := range table.Constraints {
				if constraint.Type != catalog.ConstraintTypeCheck {
					continue
				}
				rows = append(rows, []metadataValue{
					{value: informationSchemaCatalogName},
					{value: namespace.Name},
					{value: constraint.Name},
					{value: constraint.Check},
				})
			}
		}
	}
	return rows
}

func informationSchemaCharacterSetRows() [][]metadataValue {
	return [][]metadataValue{{{value: "utf8mb4"}, {value: "utf8mb4_0900_ai_ci"}, {value: "UTF-8 Unicode"}, {value: "4"}}}
}

func informationSchemaCollationRows() [][]metadataValue {
	return [][]metadataValue{
		{{value: "utf8mb4_0900_ai_ci"}, {value: "utf8mb4"}, {value: "Yes"}},
		{{value: "utf8mb4_bin"}, {value: "utf8mb4"}, {value: ""}},
	}
}

func informationSchemaAccountRows(s *session, definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, account := range visibleAccounts(s, definition) {
		locked := "0"
		if account.Locked {
			locked = "1"
		}
		rows = append(rows, []metadataValue{{value: account.Name}, {value: locked}})
	}
	return rows
}

func informationSchemaAccountGrantRows(s *session, definition catalog.Definition) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, account := range visibleAccounts(s, definition) {
		for _, grant := range sortedAccountGrants(account.Grants) {
			rows = append(rows, []metadataValue{{value: account.Name}, {value: grant.Privilege}, {value: grant.Namespace}})
		}
	}
	return rows
}

func visibleAccounts(s *session, definition catalog.Definition) []catalog.Account {
	accounts := make([]catalog.Account, 0, len(definition.Accounts))
	manager := accountIsManager(s, definition)
	for _, account := range definition.Accounts {
		if !manager && s != nil && s.username != "" && account.Name != s.username {
			continue
		}
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
	return accounts
}

func accountIsManager(s *session, definition catalog.Definition) bool {
	if s == nil || s.username == "" {
		return true
	}
	account, found := definition.Accounts[s.username]
	return found && !account.Locked && accountHasGrant(account, accountManagerPrivilege)
}

func informationSchemaProcessListRows(s *session) [][]metadataValue {
	if s == nil || s.server == nil || s.server.connections == nil {
		return nil
	}
	rows := make([][]metadataValue, 0)
	for _, snapshot := range s.server.connections.sessionSnapshots() {
		if !sessionCanObserve(s, snapshot.username) {
			continue
		}
		command, info := "Sleep", ""
		if snapshot.running {
			command, info = "Query", snapshot.query
		}
		rows = append(rows, []metadataValue{
			{value: strconv.FormatUint(uint64(snapshot.id), 10)},
			{value: snapshot.username},
			{value: ""},
			{value: snapshot.database},
			{value: command},
			{value: "0"},
			{value: ""},
			{value: info},
		})
	}
	return rows
}

func sessionCanObserve(s *session, username string) bool {
	if s.username == "" || username == s.username {
		return true
	}
	if s.server.config.Catalog == nil {
		return false
	}
	account, found := s.server.config.Catalog.Account(s.username)
	return found && !account.Locked && accountGrantIndex(account, "OPERATIONAL_OBSERVATION", "") >= 0
}
