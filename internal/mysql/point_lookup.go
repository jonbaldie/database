package mysql

import (
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
)

// relationalPointLookup is a probe of one unique key for one literal value.
// The planner and the executor use the same probe, so a Query explanation
// reports the path that the statement runs.
type relationalPointLookup struct {
	index  string
	column string
	value  string
}

func planPointLookup(plan *relationalSelectPlan) (relationalPointLookup, bool) {
	if !pointLookupEligible(plan) {
		return relationalPointLookup{}, false
	}
	column, value, ok := parseSimpleEqualityWhere(plan.whereText)
	if !ok {
		return relationalPointLookup{}, false
	}
	index, ok := pointLookupIndex(plan.source.tables[0].table, column)
	if !ok {
		return relationalPointLookup{}, false
	}
	return relationalPointLookup{index: index, column: column, value: value}, true
}

// tryPointLookup returns false when the probe finds no matching row. The
// caller then compares the stored rows, because equal values can have a
// different stored form.
func tryPointLookup(plan *relationalSelectPlan) ([]relationalResultRow, bool) {
	lookup, ok := planPointLookup(plan)
	if !ok {
		return nil, false
	}
	started := time.Now()
	row, ok := lookupStatementRow(plan, lookup.column, lookup.value)
	if !ok {
		return nil, false
	}
	rows, ok := projectPointLookup(plan, row)
	if !ok {
		return nil, false
	}
	plan.recordPointLookup(row, rows, time.Since(started))
	return rows, true
}

func (p *relationalSelectPlan) recordPointLookup(row []string, rows []relationalResultRow, elapsed time.Duration) {
	if p.runtime == nil {
		return
	}
	bytes := 0
	for _, value := range row {
		bytes += len(value)
	}
	p.runtime.recordScan(0, 1, bytes, elapsed)
	p.recordReadPipeline(1, 1, rows, elapsed)
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

func pointLookupIndex(table catalog.Table, column string) (string, bool) {
	if primaryColumn(table) == column {
		return "PRIMARY", true
	}
	if uniqueColumn(table) != column {
		return "", false
	}
	return uniqueKeyName(table, column), true
}

// uniqueKeyName names the single-column unique key on column. It uses the
// column name when the catalog records the key on the column only.
func uniqueKeyName(table catalog.Table, column string) string {
	for _, constraint := range table.Constraints {
		if constraint.Type == catalog.ConstraintTypeUnique && len(constraint.Columns) == 1 && constraint.Columns[0] == column {
			return constraint.Name
		}
	}
	for _, index := range table.Indexes {
		if index.Unique && len(index.Parts) == 1 && index.Parts[0].Column == column {
			return index.Name
		}
	}
	return column
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
