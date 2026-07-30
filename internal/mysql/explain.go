package mysql

import (
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

// explainStatement plans a supported statement without executing it and renders
// the canonical JSON document or the stable MySQL-oriented tabular projection.
func (s *textStatementExecutor) explainStatement(query string) (*queryResult, error) {
	format, inner, err := parseExplain(query)
	if err != nil {
		return nil, err
	}
	document, err := s.planExplanation(inner)
	if err != nil {
		return nil, err
	}
	return renderExplanation(format, document)
}

func parseExplain(query string) (string, string, error) {
	rest := strings.TrimSpace(query[len("explain "):])
	lower := strings.ToLower(rest)
	if strings.HasPrefix(lower, "analyze") || strings.HasPrefix(lower, "for connection") {
		return "", "", sqlFailure{1235, "42000", "EXPLAIN ANALYZE and FOR CONNECTION are not supported in v0.1 plan-only explanation"}
	}
	if !strings.HasPrefix(lower, "format") {
		return "traditional", rest, nil
	}
	return parseExplainFormat(rest)
}

func parseExplainFormat(rest string) (string, string, error) {
	after := strings.TrimSpace(rest[len("format"):])
	if !strings.HasPrefix(after, "=") {
		return "", "", sqlFailure{1064, "42000", "EXPLAIN FORMAT requires '=' before the format name"}
	}
	value, statement, ok := splitLeadingWord(strings.TrimSpace(after[1:]))
	if !ok {
		return "", "", sqlFailure{1064, "42000", "EXPLAIN FORMAT requires a statement to explain"}
	}
	switch strings.ToLower(value) {
	case "json":
		return "json", statement, nil
	case "traditional":
		return "traditional", statement, nil
	default:
		return "", "", sqlFailure{1235, "42000", "EXPLAIN supports only FORMAT=TRADITIONAL and FORMAT=JSON"}
	}
}

func splitLeadingWord(text string) (string, string, bool) {
	boundary := strings.IndexAny(text, " \t\n\r")
	if boundary < 0 {
		return "", "", false
	}
	return text[:boundary], strings.TrimSpace(text[boundary+1:]), true
}

func (s *textStatementExecutor) planExplanation(inner string) (*queryexplanation.Document, error) {
	relations := relationExecutor{session: s.session}
	lower := strings.ToLower(inner)
	switch {
	case isComposedSelectStatement(inner):
		return s.explainSelect(&relations, inner)
	case strings.HasPrefix(lower, "insert into "):
		return s.explainInsert(&relations, inner)
	case strings.HasPrefix(lower, "update "):
		return s.explainUpdate(&relations, inner)
	case strings.HasPrefix(lower, "delete from "):
		return s.explainDelete(&relations, inner)
	default:
		return nil, sqlFailure{1064, "42000", "EXPLAIN supports SELECT, INSERT, UPDATE, and DELETE statements"}
	}
}

func (s *textStatementExecutor) explainSelect(relations *relationExecutor, inner string) (*queryexplanation.Document, error) {
	return s.explainComposedSelect(relations, inner)
}

func (s *textStatementExecutor) explainScalarSelect(inner, expression string) (*queryexplanation.Document, error) {
	// A no-FROM read is only explainable when the executor would accept it, so
	// the plan is the plan that would actually run.
	if literal := parseLiteralResult(expression); !literal.supported {
		return nil, sqlFailure{1064, "42000", "unsupported expression"}
	}
	return queryexplanation.PlanScalarSelect(s.server.config.Version, inner, s.database, []string{expression}), nil
}

func (s *textStatementExecutor) explainSelectSource(relations *relationExecutor, projection, source string) (queryexplanation.Select, error) {
	target, where, ok := splitWhere(source)
	if !ok {
		return queryexplanation.Select{}, sqlFailure{1064, "42000", "malformed SELECT"}
	}
	parts, ok := splitQualifiedIdentifier(target)
	if !ok || len(parts) == 0 || len(parts) > 2 {
		return queryexplanation.Select{}, sqlFailure{1064, "42000", "invalid table name"}
	}
	namespace, tableName, table, err := explainTable(relations, parts)
	if err != nil {
		return queryexplanation.Select{}, err
	}
	indexes, err := tableColumnIndexes(table)
	if err != nil {
		return queryexplanation.Select{}, err
	}
	_, columns, err := selectedColumns(table, projection, indexes)
	if err != nil {
		return queryexplanation.Select{}, err
	}
	// Validate the predicate exactly as the executor would, so EXPLAIN never
	// describes a plan for a statement that would fail at run time.
	if err := validateExplainPredicate(relations, where, table, indexes); err != nil {
		return queryexplanation.Select{}, err
	}
	return queryexplanation.Select{
		Table:      relationInfo(namespace, tableName, table),
		Columns:    columns,
		AllColumns: projection == "*",
		Where:      strings.TrimSpace(where),
	}, nil
}

func validateExplainPredicate(relations *relationExecutor, where string, table catalog.Table, indexes map[string]int) error {
	offsetMinutes, err := sessionTimeZoneOffset(relations.session)
	if err != nil {
		return err
	}
	_, err = rowMatcherAtOffset(strings.TrimSpace(where), table, indexes, offsetMinutes)
	return err
}

// explainInsert, explainUpdate, and explainDelete reuse the executor's own plan
// builders so a plan is only produced for a statement the executor would run.
func (s *textStatementExecutor) explainInsert(relations *relationExecutor, inner string) (*queryexplanation.Document, error) {
	plan, err := makeInsertPlan(relations, inner)
	if err != nil {
		return nil, err
	}
	write := queryexplanation.Write{Kind: "insert", Table: relationInfo(plan.namespace, plan.name, plan.table), ValueRows: len(plan.groups), Constraints: explanationConstraints(relations.currentDefinition(), plan.namespace, plan.name, plan.table)}
	return queryexplanation.PlanWrite(s.server.config.Version, inner, s.database, write), nil
}

func (s *textStatementExecutor) explainUpdate(relations *relationExecutor, inner string) (*queryexplanation.Document, error) {
	plan, err := makeUpdatePlan(relations, inner)
	if err != nil {
		return nil, err
	}
	_, _, where, err := parseUpdateInput(inner)
	if err != nil {
		return nil, err
	}
	write := queryexplanation.Write{Kind: "update", Table: relationInfo(plan.namespace, plan.name, plan.table), Where: strings.TrimSpace(where), Constraints: explanationConstraints(relations.currentDefinition(), plan.namespace, plan.name, plan.table)}
	return queryexplanation.PlanWrite(s.server.config.Version, inner, s.database, write), nil
}

func (s *textStatementExecutor) explainDelete(relations *relationExecutor, inner string) (*queryexplanation.Document, error) {
	plan, err := makeDeletePlan(relations, inner)
	if err != nil {
		return nil, err
	}
	_, where, err := parseDeleteInput(inner)
	if err != nil {
		return nil, err
	}
	write := queryexplanation.Write{Kind: "delete", Table: relationInfo(plan.namespace, plan.name, plan.table), Where: strings.TrimSpace(where), Constraints: explanationConstraints(relations.currentDefinition(), plan.namespace, plan.name, plan.table)}
	return queryexplanation.PlanWrite(s.server.config.Version, inner, s.database, write), nil
}

func explanationConstraints(definition catalog.Definition, namespaceName, tableName string, table catalog.Table) []queryexplanation.Constraint {
	constraints := make([]queryexplanation.Constraint, 0, len(table.Constraints)+len(table.Columns))
	owner := relationInfo(namespaceName, tableName, table)
	for index, column := range table.Columns {
		if !catalog.ColumnAttributeAt(table, index).Nullable {
			constraints = append(constraints, queryexplanation.Constraint{Type: "not_null", Name: table.Name + "_" + column + "_not_null", Table: owner})
		}
	}
	for _, constraint := range table.Constraints {
		kind := constraint.Type
		if kind == catalog.ConstraintTypePrimary {
			kind = catalog.ConstraintTypeUnique
		}
		constraints = append(constraints, queryexplanation.Constraint{Type: kind, Name: constraint.Name, Table: owner})
	}
	return append(constraints, inboundForeignKeyConstraints(definition, namespaceName, tableName)...)
}

func inboundForeignKeyConstraints(definition catalog.Definition, namespaceName, tableName string) []queryexplanation.Constraint {
	constraints := []queryexplanation.Constraint{}
	for ownerKey, namespace := range definition.Namespaces {
		ownerName := namespace.Name
		if ownerName == "" {
			ownerName = ownerKey
		}
		for tableKey, table := range namespace.Tables {
			ownerTableName := table.Name
			if ownerTableName == "" {
				ownerTableName = tableKey
			}
			for _, constraint := range table.Constraints {
				if constraint.Type != catalog.ConstraintTypeForeignKey || !foreignKeyTargets(constraint, ownerName, namespaceName, tableName) {
					continue
				}
				constraints = append(constraints, queryexplanation.Constraint{Type: catalog.ConstraintTypeForeignKey, Name: constraint.Name, Table: relationInfo(ownerName, ownerTableName, table)})
			}
		}
	}
	return constraints
}

func foreignKeyTargets(constraint catalog.Constraint, ownerNamespace, namespaceName, tableName string) bool {
	targetNamespace := constraint.ReferencedNamespace
	if targetNamespace == "" {
		targetNamespace = ownerNamespace
	}
	return catalog.Key(targetNamespace) == catalog.Key(namespaceName) && catalog.Key(constraint.ReferencedTable) == catalog.Key(tableName)
}

func explainTable(relations *relationExecutor, parts []string) (string, string, catalog.Table, error) {
	namespace, tableName, err := tableTarget(relations, parts)
	if err != nil {
		return "", "", catalog.Table{}, err
	}
	table, err := relationTable(relations, namespace, tableName)
	if err != nil {
		return "", "", catalog.Table{}, err
	}
	return namespace, tableName, table, nil
}

func relationInfo(namespace, tableName string, table catalog.Table) queryexplanation.Table {
	return queryexplanation.Table{
		Database: namespace,
		Name:     tableName,
		Columns:  append([]string(nil), table.Columns...),
		RowCount: len(table.Rows),
	}
}

func relationSourceInfo(source relationalTableSource) queryexplanation.Table {
	info := relationInfo(source.namespace, source.name, source.table)
	if source.access != nil {
		info.Access = &queryexplanation.IndexAccess{
			Name: source.access.Name, Unique: source.access.Unique, Forced: source.forced, Covering: source.covering,
			CollectedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}
	return info
}

func renderExplanation(format string, document *queryexplanation.Document) (*queryResult, error) {
	if format == "json" {
		return explanationJSONResult(document)
	}
	return explanationTabularResult(document), nil
}

func explanationJSONResult(document *queryexplanation.Document) (*queryResult, error) {
	encoded, err := queryexplanation.RenderJSON(document)
	if err != nil {
		return nil, sqlFailure{1105, "HY000", "explanation could not be encoded"}
	}
	return &queryResult{columns: []string{"EXPLAIN"}, rows: [][]string{{encoded}}}, nil
}

func explanationTabularResult(document *queryexplanation.Document) *queryResult {
	tabular := queryexplanation.RenderTabular(document)
	return &queryResult{columns: tabular.Columns, rows: tabular.Rows, nulls: tabular.Null}
}
