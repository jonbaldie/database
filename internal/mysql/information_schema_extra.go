package mysql

import (
	"sort"
	"strconv"

	"github.com/jonbaldie/database/internal/catalog"
)

type informationSchemaRowBuilder func(*session, catalogMetadataProjection) [][]metadataValue

var informationSchemaRowBuilders = map[string]informationSchemaRowBuilder{
	"schemata":                informationSchemaSchemataRows,
	"tables":                  informationSchemaTableRows,
	"columns":                 informationSchemaColumnRows,
	"statistics":              informationSchemaStatisticsRows,
	"table_constraints":       informationSchemaTableConstraintRows,
	"key_column_usage":        informationSchemaKeyColumnUsageRows,
	"referential_constraints": informationSchemaReferentialRows,
	"check_constraints":       informationSchemaCheckRows,
	"character_sets": func(*session, catalogMetadataProjection) [][]metadataValue {
		return informationSchemaCharacterSetRows()
	},
	"collations": func(*session, catalogMetadataProjection) [][]metadataValue {
		return informationSchemaCollationRows()
	},
	"accounts":       informationSchemaAccountRows,
	"account_grants": informationSchemaAccountGrantRows,
	"processlist": func(s *session, _ catalogMetadataProjection) [][]metadataValue {
		return informationSchemaProcessListRows(s)
	},
}

func informationSchemaStatisticsRows(_ *session, projection catalogMetadataProjection) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range projection.namespaces {
		for _, table := range namespace.tables {
			for _, index := range table.indexes {
				rows = append(rows, index.informationSchemaRow(namespace.name, table.name))
			}
		}
	}
	return rows
}

func columnNullable(table catalog.Table, name string) bool {
	if name == "" {
		return true
	}
	for index, column := range table.Columns {
		if !identifiersEqual(column, name) {
			continue
		}
		if index < len(table.ColumnAttributes) {
			return table.ColumnAttributes[index].Nullable
		}
		return true
	}
	return true
}

func informationSchemaTableConstraintRows(_ *session, projection catalogMetadataProjection) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range projection.namespaces {
		for _, table := range namespace.tables {
			for _, constraint := range table.constraints {
				rows = append(rows, []metadataValue{
					{value: namespace.name},
					{value: constraint.definition.Name},
					{value: namespace.name},
					{value: table.name},
					{value: constraint.typeLabel()},
				})
			}
		}
	}
	return rows
}

func informationSchemaKeyColumnUsageRows(_ *session, projection catalogMetadataProjection) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range projection.namespaces {
		for _, table := range namespace.tables {
			for _, constraint := range table.constraints {
				rows = append(rows, keyColumnUsageRows(namespace.name, table.name, constraint.definition)...)
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
		if constraint.Type == catalog.ConstraintTypeForeignKey {
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
			{value: namespace},
			{value: constraint.Name},
			{value: namespace},
			{value: table},
			{value: column},
			{value: strconv.Itoa(index + 1)},
			referencedSchema,
			referencedTable,
			referencedColumn,
		})
	}
	return rows
}

func informationSchemaReferentialRows(_ *session, projection catalogMetadataProjection) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range projection.namespaces {
		for _, table := range namespace.tables {
			for _, constraint := range table.constraints {
				if constraint.definition.Type != catalog.ConstraintTypeForeignKey {
					continue
				}
				rows = append(rows, []metadataValue{
					{value: namespace.name},
					{value: constraint.definition.Name},
					{value: table.name},
					{value: constraint.definition.ReferencedTable},
				})
			}
		}
	}
	return rows
}

func informationSchemaCheckRows(_ *session, projection catalogMetadataProjection) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, namespace := range projection.namespaces {
		for _, table := range namespace.tables {
			for _, constraint := range table.constraints {
				if constraint.definition.Type != catalog.ConstraintTypeCheck {
					continue
				}
				rows = append(rows, []metadataValue{
					{value: namespace.name},
					{value: constraint.definition.Name},
					{value: constraint.definition.Check},
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

func informationSchemaAccountRows(s *session, projection catalogMetadataProjection) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, account := range visibleAccounts(s, projection.definition) {
		locked := "0"
		if account.Locked {
			locked = "1"
		}
		rows = append(rows, []metadataValue{{value: account.Name}, {value: locked}})
	}
	return rows
}

func informationSchemaAccountGrantRows(s *session, projection catalogMetadataProjection) [][]metadataValue {
	rows := make([][]metadataValue, 0)
	for _, account := range visibleAccounts(s, projection.definition) {
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
