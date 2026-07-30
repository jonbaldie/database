package mysql

import (
	"errors"
	"sort"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

// relationalSelectPlan is the shared, statement-scoped shape used by SELECT
// execution and EXPLAIN. Tables are resolved from the session definition once;
// all later operators consume that same immutable relation image.
type relationalSelectPlan struct {
	relationalSelectEnvironment
	projection  []relationalProjection
	allColumns  bool
	distinct    bool
	source      relationalSource
	where       relationPredicate
	whereText   string
	aggregation relationalAggregation
	order       []relationalOrder
	limit       relationalLimit
}

type relationalSelectEnvironment struct {
	session  *session
	composed *composedQueryContext
	outer    *outerRelationScope
}

type relationalSource struct {
	tables  []relationalTableSource
	joins   []relationalJoin
	columns []relationColumn
}

type relationalTableSource struct {
	namespace string
	name      string
	alias     string
	table     catalog.Table
	columns   []relationColumn
	query     string
	reason    string
}

type relationalJoin struct {
	kind      string
	right     relationalTableSource
	condition string
	using     []string
	predicate relationPredicate
	columns   []relationColumn
}

type relationColumn struct {
	namespace       string
	table           string
	qualifier       string
	name            string
	typeName        string
	index           int
	coalesce        int
	hidden          bool
	metadata        columnMetadata
	tableDefinition catalog.Table
}

type relationalProjection struct {
	expression  string
	name        string
	alias       string
	column      int
	scalar      bool
	computed    bool
	subquery    string
	context     *composedQueryContext
	value       exprValue
	metadata    columnMetadata
	outer       *outerRelationScope
	aggregate   *relationalAggregate
	window      *relationalWindowFunction
	windowExpr  string
	windowParts []relationalComposedWindow
}

// relationalComposedWindow is one window result that a scalar projection uses.
// The placeholder is a private identifier in the scalar expression.
type relationalComposedWindow struct {
	function    relationalWindowFunction
	placeholder string
	metadata    columnMetadata
}

type relationalOrder struct {
	expression     string
	direction      string
	column         int
	projection     int
	fromProjection bool
	computed       bool
}

type relationalLimit struct {
	present bool
	offset  int
	count   int
}

type relationRow struct{ values []string }

type relationalResultRow struct {
	values      []string
	nulls       []bool
	source      relationRow
	group       []relationRow
	projections []exprValue
	orders      []exprValue
}

func selectQuery(s *relationExecutor, query string) (*queryResult, error) {
	context := s.composed
	if context == nil {
		context = newComposedQueryContext(s)
	}
	return executeComposedSelect(context, query, nil)
}

func executeRelationalSelect(s *relationExecutor, query string) (*queryResult, error) {
	return executeRelationalSelectContext(s, query, nil)
}

func executeRelationalSelectContext(s *relationExecutor, query string, outer *outerRelationScope) (*queryResult, error) {
	plan, err := parseRelationalSelectContext(s, query, outer)
	if err != nil {
		return nil, err
	}
	if s.streamRows && !plan.requiresMaterialization() {
		return plan.streamingResult(), nil
	}
	resultRows, err := collectRelationalResultRows(plan)
	if err != nil {
		return nil, err
	}
	return plan.result(plan.shapeRows(resultRows)), nil
}

func (p *relationalSelectPlan) hasRuntimeSubqueries() bool {
	for _, projection := range p.projection {
		if projection.subquery != "" {
			return true
		}
	}
	if len(parenthesizedSelectQueries(p.whereText)) > 0 {
		return true
	}
	for _, join := range p.source.joins {
		if len(parenthesizedSelectQueries(join.condition)) > 0 {
			return true
		}
	}
	return false
}

func (p *relationalSelectPlan) requiresMaterialization() bool {
	return p.hasRuntimeSubqueries() || p.hasAggregateOrWindow()
}

func collectRelationalResultRows(plan *relationalSelectPlan) ([]relationalResultRow, error) {
	if plan.hasAggregateOrWindow() {
		return collectAggregateOrWindowRows(plan)
	}
	resultRows := make([]relationalResultRow, 0)
	err := plan.forEachSourceRow(func(row relationRow) error {
		if plan.where != nil {
			matched, err := predicateMatches(plan.where, row)
			if err != nil {
				return err
			}
			if !matched {
				return nil
			}
		}
		result, err := plan.projectRow(row)
		if err != nil {
			return err
		}
		resultRows = append(resultRows, result)
		return nil
	})
	return resultRows, err
}

var errStopRelationStream = errors.New("relational row stream complete")

type relationalResultStream struct {
	plan    *relationalSelectPlan
	skipped int
	emitted int
}

func (p *relationalSelectPlan) streamingResult() *queryResult {
	columns, metadata := p.resultColumns()
	stream := &relationalResultStream{plan: p}
	return &queryResult{columns: columns, metadata: metadata, stream: stream.rows}
}

func (s *relationalResultStream) rows(yield func([]string, []bool) error) error {
	if s.plan.distinct || len(s.plan.order) > 0 {
		rows, err := collectRelationalResultRows(s.plan)
		if err != nil {
			return err
		}
		return s.yieldRows(s.plan.shapeRows(rows), yield)
	}
	return s.incrementalRows(yield)
}

func (s *relationalResultStream) incrementalRows(yield func([]string, []bool) error) error {
	if s.plan.limit.present && s.plan.limit.count == 0 {
		return nil
	}
	err := s.plan.forEachSourceRow(func(row relationRow) error {
		return s.yieldSourceRow(row, yield)
	})
	if errors.Is(err, errStopRelationStream) {
		return nil
	}
	return err
}

func (s *relationalResultStream) yieldSourceRow(row relationRow, yield func([]string, []bool) error) error {
	matched, err := relationalRowMatches(s.plan.where, row)
	if err != nil || !matched {
		return err
	}
	if s.plan.limit.present && s.skipped < s.plan.limit.offset {
		s.skipped++
		return nil
	}
	result, err := s.plan.projectRow(row)
	if err != nil {
		return err
	}
	values, nulls := s.outputRow(result)
	if err := yield(values, nulls); err != nil {
		return err
	}
	s.emitted++
	if s.plan.limit.present && s.emitted >= s.plan.limit.count {
		return errStopRelationStream
	}
	return nil
}

func relationalRowMatches(predicate relationPredicate, row relationRow) (bool, error) {
	if predicate == nil {
		return true, nil
	}
	return predicateMatches(predicate, row)
}

func (s *relationalResultStream) yieldRows(rows []relationalResultRow, yield func([]string, []bool) error) error {
	for _, row := range rows {
		values, nulls := s.outputRow(row)
		if err := yield(values, nulls); err != nil {
			return err
		}
	}
	return nil
}

func (s *relationalResultStream) outputRow(row relationalResultRow) ([]string, []bool) {
	values := append([]string(nil), row.values...)
	nulls := append([]bool(nil), row.nulls...)
	s.plan.renderTemporalResults([][]string{values}, [][]bool{nulls})
	displayStoredNulls([][]string{values})
	return values, nulls
}

func parseRelationalSelect(s *relationExecutor, query string) (*relationalSelectPlan, error) {
	return parseRelationalSelectContext(s, query, nil)
}

func parseRelationalSelectContext(s *relationExecutor, query string, outer *outerRelationScope) (*relationalSelectPlan, error) {
	expression, err := selectExpression(query)
	if err != nil {
		return nil, err
	}
	distinct, projectionText, tail, err := splitSelectProjection(expression)
	if err != nil {
		return nil, err
	}
	sourceText, whereText, groupText, havingText, windowText, orderText, limitText, err := splitSelectTail(tail)
	if err != nil {
		return nil, err
	}
	source, err := parseRelationalSource(s, sourceText)
	if err != nil {
		return nil, err
	}
	plan := &relationalSelectPlan{relationalSelectEnvironment: relationalSelectEnvironment{session: s.session, composed: s.composed, outer: outer}, distinct: distinct, source: source, whereText: strings.TrimSpace(whereText)}
	if err := finishRelationalSelectPlan(plan, projectionText, groupText, havingText, windowText, orderText, limitText); err != nil {
		return nil, err
	}
	return plan, nil
}

func finishRelationalSelectPlan(plan *relationalSelectPlan, projection, group, having, windows, order, limit string) error {
	if err := plan.compileProjection(projection); err != nil {
		return err
	}
	if err := plan.compilePredicates(); err != nil {
		return err
	}
	if err := plan.compileAggregation(group, having, windows); err != nil {
		return err
	}
	parsedOrder, err := parseRelationalOrder(order, plan.projection, plan.source.columns)
	if err != nil {
		return err
	}
	if err := plan.validateGroupedOrders(parsedOrder); err != nil {
		return err
	}
	if err := validateDistinctOrder(plan.distinct, parsedOrder, plan.projection); err != nil {
		return err
	}
	parsedLimit, err := parseRelationalLimit(limit)
	if err != nil {
		return err
	}
	plan.order, plan.limit = parsedOrder, parsedLimit
	return nil
}

func validateDistinctOrder(distinct bool, orders []relationalOrder, projections []relationalProjection) error {
	if !distinct {
		return nil
	}
	for _, order := range orders {
		if order.fromProjection || projectedColumn(order.column, projections) {
			continue
		}
		return sqlFailure{3065, "HY000", "ORDER BY expression is not in SELECT list; this is incompatible with DISTINCT"}
	}
	return nil
}

func projectedColumn(column int, projections []relationalProjection) bool {
	for _, projection := range projections {
		if !projection.scalar && !projection.computed && projection.column == column {
			return true
		}
	}
	return false
}

func selectExpression(query string) (string, error) {
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	if !strings.HasPrefix(strings.ToLower(query), "select ") {
		return "", unsupportedExpression()
	}
	return strings.TrimSpace(query[len("SELECT "):]), nil
}

func splitSelectProjection(expression string) (bool, string, string, error) {
	distinct, projection := parseDistinctProjection(expression)
	from := keywordAt(projection, "from")
	if from < 0 {
		return false, "", "", sqlFailure{1064, "42000", "SELECT requires a FROM clause"}
	}
	return distinct, strings.TrimSpace(projection[:from]), strings.TrimSpace(projection[from+len("from"):]), nil
}

func (p *relationalSelectPlan) compileProjection(text string) error {
	projection, allColumns, err := parseRelationalProjection(text, p.source.columns, p.composed, p.outer)
	if err != nil {
		return err
	}
	for index := range projection {
		projection[index] = projection[index].resolveName(p.source.columns)
	}
	p.projection, p.allColumns = projection, allColumns
	return nil
}

func (p *relationalSelectPlan) compilePredicates() error {
	if p.whereText != "" {
		predicate, err := compileRelationPredicateContext(p.whereText, p.source.columns, p.session, p.composed, p.outer)
		if err != nil {
			return err
		}
		p.where = predicate
	}
	for index := range p.source.joins {
		if p.source.joins[index].condition == "" {
			continue
		}
		predicate, err := compileRelationPredicateContext(p.source.joins[index].condition, p.source.joins[index].columns, p.session, p.composed, p.outer)
		if err != nil {
			return err
		}
		p.source.joins[index].predicate = predicate
	}
	return nil
}

func parseDistinctProjection(expression string) (bool, string) {
	lower := strings.ToLower(strings.TrimSpace(expression))
	if !strings.HasPrefix(lower, "distinct ") {
		return false, expression
	}
	return true, strings.TrimSpace(expression[len("distinct "):])
}

func splitSelectTail(tail string) (string, string, string, string, string, string, string, error) {
	clauses := selectClauses(tail)
	sort.Slice(clauses, func(i, j int) bool { return clauses[i].at < clauses[j].at })
	if len(clauses) == 0 {
		return strings.TrimSpace(tail), "", "", "", "", "", "", nil
	}
	if clauses[0].at == 0 {
		return "", "", "", "", "", "", "", sqlFailure{1064, "42000", "malformed SELECT source"}
	}
	values := map[string]string{}
	for index, current := range clauses {
		end := len(tail)
		if index+1 < len(clauses) {
			end = clauses[index+1].at
		}
		value, err := selectClauseValue(tail, current.name, current.at, end)
		if err != nil {
			return "", "", "", "", "", "", "", err
		}
		values[current.name] = value
	}
	return strings.TrimSpace(tail[:clauses[0].at]), values["where"], values["group"], values["having"], values["window"], values["order"], values["limit"], nil
}

func selectClauses(tail string) []struct {
	name string
	at   int
} {
	clauses := make([]struct {
		name string
		at   int
	}, 0, 6)
	for _, candidate := range []string{"where", "group", "having", "window", "order", "limit"} {
		if at := keywordAt(tail, candidate); at >= 0 {
			clauses = append(clauses, struct {
				name string
				at   int
			}{name: candidate, at: at})
		}
	}
	return clauses
}

func selectClauseValue(tail, name string, start, end int) (string, error) {
	value := strings.TrimSpace(tail[start+len(name) : end])
	if name == "order" || name == "group" {
		if !strings.HasPrefix(strings.ToLower(value), "by ") {
			return "", sqlFailure{1064, "42000", strings.ToUpper(name) + " requires BY"}
		}
		value = strings.TrimSpace(value[len("by "):])
	}
	if value == "" {
		return "", sqlFailure{1064, "42000", "malformed SELECT clause"}
	}
	return value, nil
}

func predicateMatches(predicate relationPredicate, row relationRow) (bool, error) {
	value, err := predicate(row)
	if err != nil {
		return false, err
	}
	known, truth, err := truthValue(value)
	return known && truth, err
}

func (p *relationalSelectPlan) result(rows []relationalResultRow) *queryResult {
	columns, metadata := p.resultColumns()
	resultRows, nulls := copyResultRows(rows)
	p.renderTemporalResults(resultRows, nulls)
	displayStoredNulls(resultRows)
	return &queryResult{columns: columns, rows: resultRows, nulls: nulls, metadata: metadata}
}

func (p *relationalSelectPlan) resultColumns() ([]string, []columnMetadata) {
	columns := make([]string, len(p.projection))
	metadata := make([]columnMetadata, len(p.projection))
	for index, projection := range p.projection {
		columns[index], metadata[index] = projection.name, projection.metadata
	}
	return columns, metadata
}

func copyResultRows(rows []relationalResultRow) ([][]string, [][]bool) {
	resultRows := make([][]string, len(rows))
	nulls := make([][]bool, len(rows))
	for index, row := range rows {
		resultRows[index] = append([]string(nil), row.values...)
		nulls[index] = append([]bool(nil), row.nulls...)
	}
	return resultRows, nulls
}

func (p *relationalSelectPlan) renderTemporalResults(rows [][]string, nulls [][]bool) {
	offset, err := sessionTimeZoneOffset(p.session)
	if err != nil {
		return
	}
	for index, projection := range p.projection {
		if !projectionNeedsTemporalRendering(projection) {
			continue
		}
		column := p.source.columns[projection.column]
		typ, err := parseTemporalType(column.typeName)
		if err != nil || typ.kind != temporalTimestamp {
			continue
		}
		renderTemporalColumn(rows, nulls, index, offset, typ.precision)
	}
}

func projectionNeedsTemporalRendering(projection relationalProjection) bool {
	return !projection.scalar && !projection.computed && projection.subquery == "" && projection.aggregate == nil && projection.window == nil
}

func renderTemporalColumn(rows [][]string, nulls [][]bool, column, offset, precision int) {
	for rowIndex := range rows {
		if nulls[rowIndex][column] {
			continue
		}
		rows[rowIndex][column], _ = renderTimestampFixedOffset(rows[rowIndex][column], offset, precision)
	}
}

func (p *relationalSelectPlan) explanation(serverVersion, currentDatabase, sql string) *queryexplanation.Document {
	read := queryexplanation.Select{
		Columns:               projectionNames(p.projection),
		ProjectionExpressions: projectionExpressions(p.projection, p.source.columns),
		AllColumns:            p.allColumns,
		Where:                 p.whereText,
		GroupExpressions:      groupExpressions(p.aggregation.groups),
		Having:                p.aggregation.having,
		AggregateCount:        aggregateProjectionCount(p.projection),
		Window:                queryexplanation.WindowDetails{Count: windowDefinitionCount(p.projection), FunctionCount: windowProjectionCount(p.projection), Definitions: windowExplanationDefinitions(p.projection)},
		Distinct:              p.distinct,
		Orders:                explanationOrders(p.order),
		Limit:                 queryexplanation.Limit{Present: p.limit.present, Offset: p.limit.offset, Count: p.limit.count},
	}
	for _, table := range p.source.tables {
		read.Tables = append(read.Tables, relationInfo(table.namespace, table.name, table.table))
	}
	if len(read.Tables) > 0 {
		read.Table = read.Tables[0]
	}
	for _, join := range p.source.joins {
		clause, fragment := "on", join.condition
		if len(join.using) > 0 {
			clause, fragment = "using", "("+strings.Join(join.using, ", ")+")"
		}
		read.Joins = append(read.Joins, queryexplanation.Join{
			Type: join.kind, Table: relationInfo(join.right.namespace, join.right.name, join.right.table), Condition: join.condition,
			SourceClause: clause, SourceFragment: fragment,
		})
	}
	return queryexplanation.PlanSelect(serverVersion, sql, currentDatabase, read)
}

func groupExpressions(groups []relationalGroup) []string {
	expressions := make([]string, len(groups))
	for index, group := range groups {
		expressions[index] = group.source
	}
	return expressions
}

func aggregateProjectionCount(projections []relationalProjection) int {
	count := 0
	for _, projection := range projections {
		if projection.aggregate != nil {
			count++
		}
	}
	return count
}

func windowProjectionCount(projections []relationalProjection) int {
	count := 0
	for _, projection := range projections {
		count += len(projectionWindowFunctions(projection))
	}
	return count
}

func projectionNames(projection []relationalProjection) []string {
	names := make([]string, len(projection))
	for index, item := range projection {
		names[index] = item.name
	}
	return names
}

func projectionExpressions(projection []relationalProjection, columns []relationColumn) []string {
	expressions := make([]string, len(projection))
	for index, item := range projection {
		expressions[index] = item.expression
		if expressions[index] == "" && item.column >= 0 && item.column < len(columns) {
			expressions[index] = columns[item.column].qualifier + "." + columns[item.column].name
		}
	}
	return expressions
}

func explanationOrders(order []relationalOrder) []queryexplanation.Order {
	result := make([]queryexplanation.Order, len(order))
	for index, item := range order {
		result[index] = queryexplanation.Order{Expression: item.expression, Direction: item.direction}
	}
	return result
}
