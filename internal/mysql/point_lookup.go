package mysql

import (
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

func tryPointLookup(plan *relationalSelectPlan) ([]relationalResultRow, bool) {
	if !pointLookupEligible(plan) {
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
	return projectPointLookup(plan, row)
}

func pointLookupEligible(plan *relationalSelectPlan) bool {
	if plan == nil || plan.session == nil || plan.session.server.config.Catalog == nil {
		return false
	}
	if len(plan.source.tables) != 1 || len(plan.source.joins) != 0 || plan.source.locking != nil {
		return false
	}
	return !plan.hasAggregateOrWindow() && !plan.distinct && len(plan.order) == 0
}

func projectPointLookup(plan *relationalSelectPlan, row []string) ([]relationalResultRow, bool) {
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
	lower := strings.ToLower(where)
	if where == "" || strings.Contains(lower, " and ") || strings.Contains(lower, " or ") {
		return "", "", false
	}
	parts := strings.SplitN(where, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	column := stripIdentifier(strings.TrimSpace(parts[0]))
	value := decodeWhereLiteral(strings.TrimSpace(parts[1]))
	if column == "" || value == "" {
		return "", "", false
	}
	return column, value, true
}

func decodeWhereLiteral(value string) string {
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return value
}

func stripIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.LastIndex(value, "."); idx >= 0 {
		value = value[idx+1:]
	}
	return strings.Trim(value, "`")
}
