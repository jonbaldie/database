package mysql

import (
	"strconv"
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

// explainStatement plans a supported statement without executing it and renders
// the canonical JSON document or the stable MySQL-oriented tabular projection.
func (s *textStatementExecutor) explainStatement(query string) (*queryResult, error) {
	request, err := parseExplain(query)
	if err != nil {
		return nil, err
	}
	switch request.mode {
	case "analyze":
		return s.analyzeExplanation(request.format, request.inner)
	case "snapshot":
		return s.snapshotExplanation(request.format, request.connectionID)
	default:
		document, err := s.planExplanation(request.inner)
		if err != nil {
			return nil, err
		}
		return renderExplanation(request.format, document)
	}
}

type explainRequest struct {
	mode         string
	format       string
	inner        string
	connectionID uint32
}

func parseExplain(query string) (explainRequest, error) {
	rest := strings.TrimSpace(query[len("explain "):])
	lower := strings.ToLower(rest)
	if strings.HasPrefix(lower, "analyze") && (len(rest) == len("analyze") || isWhitespace(rest[len("analyze")])) {
		format, inner, err := parseExplainTarget(strings.TrimSpace(rest[len("analyze"):]))
		if err != nil {
			return explainRequest{}, err
		}
		return explainRequest{mode: "analyze", format: format, inner: inner}, nil
	}
	format, inner, err := parseExplainTarget(rest)
	if err != nil {
		return explainRequest{}, err
	}
	if strings.HasPrefix(strings.ToLower(inner), "for connection") {
		connectionID, err := parseExplainConnection(inner)
		if err != nil {
			return explainRequest{}, err
		}
		return explainRequest{mode: "snapshot", format: format, connectionID: connectionID}, nil
	}
	return explainRequest{mode: "plan", format: format, inner: inner}, nil
}

func parseExplainTarget(rest string) (string, string, error) {
	if strings.TrimSpace(rest) == "" {
		return "", "", sqlFailure{1064, "42000", "EXPLAIN requires a statement"}
	}
	if !strings.HasPrefix(strings.ToLower(rest), "format") {
		return "traditional", rest, nil
	}
	return parseExplainFormat(rest)
}

func isWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func parseExplainConnection(value string) (uint32, error) {
	rest := strings.TrimSpace(value[len("for connection"):])
	if rest == "" || strings.ContainsAny(rest, " \t\n\r") {
		return 0, sqlFailure{1064, "42000", "EXPLAIN FOR CONNECTION requires one positive connection ID"}
	}
	connectionID, err := strconv.ParseUint(rest, 10, 32)
	if err != nil || connectionID == 0 {
		return 0, sqlFailure{1064, "42000", "EXPLAIN FOR CONNECTION requires one positive connection ID"}
	}
	return uint32(connectionID), nil
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

func (s *textStatementExecutor) analyzeExplanation(format, inner string) (*queryResult, error) {
	planningStarted := time.Now()
	document, err := s.planExplanation(inner)
	if err != nil {
		return nil, err
	}
	document.Timing.PlanningMS = float64(time.Since(planningStarted)) / float64(time.Millisecond)
	if document.Statement.Kind != "select" || document.Statement.LockingRead {
		return nil, sqlFailure{1235, "42000", "EXPLAIN ANALYZE supports only non-locking SELECT statements"}
	}
	started := time.Now()
	runner := *s
	runner.streamRows = false
	metrics := queryexplanation.NewRuntimeMetrics(document)
	runner.session.runtimeMetrics = metrics
	defer func() { runner.session.runtimeMetrics = nil }()
	result, err := newStatementExecutionPolicy(&runner).execute(inner, strings.ToLower(inner))
	if err != nil {
		return nil, err
	}
	rows, memory, err := discardResultRows(result)
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(started)
	recordRuntimeResources(metrics, runner.session.resourceSnapshot())
	metrics.SetRoot(rows, memory, elapsed, 0, true)
	return renderExplanation(format, queryexplanation.AnalyzeWithMetrics(document, elapsed, metrics))
}

func discardResultRows(result *queryResult) (int, int, error) {
	if result == nil {
		return 0, 0, nil
	}
	if result.stream == nil {
		return len(result.rows), queryResultMemory(result.rows, result.nulls), nil
	}
	rows := 0
	memory := 0
	err := result.stream(func(values []string, nulls []bool) error {
		rows++
		memory += queryResultMemory([][]string{values}, [][]bool{nulls})
		return nil
	})
	return rows, memory, err
}

func queryResultMemory(rows [][]string, nulls [][]bool) int {
	bytes := 0
	for index, row := range rows {
		if index < len(nulls) {
			bytes += len(nulls[index])
		}
		for _, value := range row {
			bytes += len(value)
		}
	}
	return bytes
}

func (s *textStatementExecutor) snapshotExplanation(format string, connectionID uint32) (*queryResult, error) {
	document, found := s.server.explanations.snapshot(connectionID)
	if !found {
		return nil, sqlFailure{1094, "HY000", "unknown or inactive connection ID"}
	}
	return renderExplanation(format, document)
}

func (s *textStatementExecutor) planExplanation(inner string) (*queryexplanation.Document, error) {
	relations := relationExecutor{session: s.session}
	lower := strings.ToLower(inner)
	var document *queryexplanation.Document
	var err error
	switch {
	case isComposedSelectStatement(inner):
		document, err = s.explainSelect(&relations, inner)
	case strings.HasPrefix(lower, "insert into "):
		document, err = s.explainInsert(&relations, inner)
	case strings.HasPrefix(lower, "replace "):
		document, err = s.explainReplace(&relations, inner)
	case strings.HasPrefix(lower, "update "):
		document, err = s.explainUpdate(&relations, inner)
	case strings.HasPrefix(lower, "delete from "):
		document, err = s.explainDelete(&relations, inner)
	default:
		return nil, sqlFailure{1064, "42000", "EXPLAIN supports SELECT, INSERT, REPLACE, UPDATE, and DELETE statements"}
	}
	if err != nil {
		return nil, err
	}
	document.Statement.PlanningSettings = s.planningSettings()
	return document, nil
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
	input, assignments, upsert, err := splitInsertOnDuplicate(inner)
	if err != nil {
		return nil, err
	}
	plan, err := makeInsertionExplanationPlan(relations, input)
	if err != nil {
		return nil, err
	}
	if upsert {
		indexes, err := tableColumnIndexes(plan.table)
		if err != nil {
			return nil, err
		}
		if _, err := parseUpsertAssignments(assignments, indexes); err != nil {
			return nil, err
		}
	}
	source, err := s.explanationInsertSource(relations, plan)
	if err != nil {
		return nil, err
	}
	write := queryexplanation.Write{Kind: "insert", Table: relationInfo(plan.namespace, plan.name, plan.table), ValueRows: len(plan.groups), Constraints: explanationConstraints(relations.currentDefinition(), plan.namespace, plan.name, plan.table), Source: source, Upsert: upsert}
	return queryexplanation.PlanWrite(s.server.config.Version, inner, s.database, write), nil
}

func (s *textStatementExecutor) explainReplace(relations *relationExecutor, inner string) (*queryexplanation.Document, error) {
	plan, err := makeReplaceExplanationPlan(relations, inner)
	if err != nil {
		return nil, err
	}
	source, err := s.explanationInsertSource(relations, plan)
	if err != nil {
		return nil, err
	}
	write := queryexplanation.Write{Kind: "replace", Table: relationInfo(plan.namespace, plan.name, plan.table), ValueRows: len(plan.groups), Constraints: explanationConstraints(relations.currentDefinition(), plan.namespace, plan.name, plan.table), Source: source}
	return queryexplanation.PlanWrite(s.server.config.Version, inner, s.database, write), nil
}

func (s *textStatementExecutor) explanationInsertSource(relations *relationExecutor, plan insertPlan) (*queryexplanation.Operator, error) {
	if plan.sourceSQL == "" {
		return nil, nil
	}
	document, err := s.explainComposedSelect(relations, plan.sourceSQL)
	if err != nil {
		return nil, err
	}
	return document.Plan, nil
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
