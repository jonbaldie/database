package mysql

import (
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

func tryPointLookup(plan *relationalSelectPlan) ([]relationalResultRow, bool) {
	if plan == nil || plan.session == nil || plan.session.server.config.Catalog == nil {
		return nil, false
	}
	if len(plan.source.tables) != 1 || len(plan.source.joins) != 0 || plan.source.locking != nil {
		return nil, false
	}
	if plan.hasAggregateOrWindow() || plan.distinct || len(plan.order) > 0 {
		return nil, false
	}
	column, value, ok := parseSimpleEqualityWhere(plan.whereText)
	if !ok {
		return nil, false
	}
	table := plan.source.tables[0]
	row, ok := lookupCatalogRow(plan.session.server.config.Catalog, table, column, value)
	if !ok {
		return nil, false
	}
	result := relationRow{values: row}
	if plan.where != nil {
		matched, err := predicateMatches(plan.where, result)
		if err != nil || !matched {
			return nil, false
		}
	}
	projected, err := plan.projectRow(result)
	if err != nil {
		return nil, false
	}
	return []relationalResultRow{projected}, true
}

func lookupCatalogRow(store *catalog.Store, table relationalTableSource, column, value string) ([]string, bool) {
	rows := store.Rows()
	if rows == nil {
		return nil, false
	}
	if primaryColumn(table.table) == column {
		return rows.LookupPrimary(table.namespace, table.name, value)
	}
	if uniqueColumn(table.table) == column {
		return rows.LookupUnique(table.namespace, table.name, column, value)
	}
	return nil, false
}

func primaryColumn(table catalog.Table) string {
	for _, constraint := range table.Constraints {
		if constraint.Type == catalog.ConstraintTypePrimary && len(constraint.Columns) == 1 {
			return constraint.Columns[0]
		}
	}
	return ""
}

func uniqueColumn(table catalog.Table) string {
	for _, constraint := range table.Constraints {
		if constraint.Type == catalog.ConstraintTypeUnique && len(constraint.Columns) == 1 {
			return constraint.Columns[0]
		}
	}
	for _, index := range table.Indexes {
		if index.Unique && len(index.Parts) == 1 && index.Parts[0].Column != "" {
			return index.Parts[0].Column
		}
	}
	return ""
}

func parseSimpleEqualityWhere(where string) (string, string, bool) {
	where = strings.TrimSpace(where)
	if where == "" || strings.Contains(strings.ToLower(where), " and ") || strings.Contains(strings.ToLower(where), " or ") {
		return "", "", false
	}
	parts := strings.SplitN(where, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	column := stripIdentifier(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if column == "" || value == "" {
		return "", "", false
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
		value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return column, value, true
}

func stripIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.LastIndex(value, "."); idx >= 0 {
		value = value[idx+1:]
	}
	return strings.Trim(value, "`")
}
