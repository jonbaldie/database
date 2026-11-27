package mysql

import (
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

func tryPointLookup(plan *relationalSelectPlan) ([]relationalResultRow, int, bool) {
	if !pointLookupEligible(plan) {
		return nil, 0, false
	}
	column, value, ok := parseSimpleEqualityWhere(plan.whereText)
	if !ok {
		return nil, 0, false
	}
	row, ok := lookupStatementRow(plan, column, value)
	if !ok {
		return nil, 0, false
	}
	projected, ok := projectPointLookup(plan, row)
	if !ok {
		return nil, 0, false
	}
	return projected, sourceRowBytes([][]string{row}), true
}

func applyPointLookupExplanation(p *relationalSelectPlan, read *queryexplanation.Select) {
	if !usesPointLookup(p) || len(read.Tables) == 0 {
		return
	}
	if read.Tables[0].Access == nil {
		read.Tables[0].Access = &queryexplanation.IndexAccess{
			Name:        pointLookupIndexName(p.source.tables[0].table, p.whereText),
			Unique:      true,
			CollectedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}
	read.Tables[0].Access.PointLookup = true
}

func usesPointLookup(p *relationalSelectPlan) bool {
	if !pointLookupEligible(p) {
		return false
	}
	column, _, ok := parseSimpleEqualityWhere(p.whereText)
	if !ok {
		return false
	}
	table := p.source.tables[0].table
	return primaryColumn(table) == column || uniqueColumn(table) == column
}

func pointLookupIndexName(table catalog.Table, where string) string {
	column, _, ok := parseSimpleEqualityWhere(where)
	if !ok || primaryColumn(table) == column {
		return "PRIMARY"
	}
	if name := uniqueConstraintIndexName(table.Constraints, column); name != "" {
		return name
	}
	if name := uniqueTableIndexName(table.Indexes, column); name != "" {
		return name
	}
	return column
}

func uniqueConstraintIndexName(constraints []catalog.Constraint, column string) string {
	for _, constraint := range constraints {
		if constraint.Type == catalog.ConstraintTypeUnique && len(constraint.Columns) == 1 && constraint.Columns[0] == column {
			if constraint.Name != "" {
				return constraint.Name
			}
			return column
		}
	}
	return ""
}

func uniqueTableIndexName(indexes []catalog.Index, column string) string {
	for _, index := range indexes {
		if index.Unique && len(index.Parts) == 1 && index.Parts[0].Column == column {
			return index.Name
		}
	}
	return ""
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

// lookupStatementRow reads one keyed row from the same definition that the
// general SELECT path scans. A transaction stages its mutations in that
// definition only, so the durable index serves sessions outside one.
func lookupStatementRow(plan *relationalSelectPlan, column, value string) ([]string, bool) {
	table := plan.source.tables[0]
	if plan.session.transaction {
		return lookupDefinitionRow(table, column, value)
	}
	return lookupCatalogRow(plan.session.server.config.Catalog, table, column, value)
}

func lookupDefinitionRow(table relationalTableSource, column, value string) ([]string, bool) {
	if primaryColumn(table.table) != column && uniqueColumn(table.table) != column {
		return nil, false
	}
	position := -1
	for index, name := range table.table.Columns {
		if name == column {
			position = index
			break
		}
	}
	if position < 0 {
		return nil, false
	}
	for _, row := range table.table.Rows {
		if position < len(row) && row[position] == value {
			return append([]string(nil), row...), true
		}
	}
	return nil, false
}

func lookupCatalogRow(store *catalog.Store, table relationalTableSource, column, value string) ([]string, bool) {
	identity := catalog.NewTableRef(table.namespace, table.name)
	if primaryColumn(table.table) == column {
		return store.LookupPrimary(identity, value)
	}
	if uniqueColumn(table.table) == column {
		return store.LookupUnique(identity, column, value)
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
