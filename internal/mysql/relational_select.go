package mysql

import (
	"errors"
	"sort"
	"strings"
	"time"

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
	session    *session
	composed   *composedQueryContext
	outer      *outerRelationScope
	runtimeKey string
	runtime    *selectRuntimeBinding
}

type relationalSource struct {
	tables  []relationalTableSource
	joins   []relationalJoin
	columns []relationColumn
	locking *lockingRead
	runtime *selectRuntimeBinding
}

// selectRuntimeBinding connects executable stages to the immutable physical
// tree. It stores IDs, rather than names, so repeated execution accumulates
// on the same physical operator.
type selectRuntimeBinding struct {
	metrics      *queryexplanation.RuntimeMetrics
	scans        []int
	joins        []int
	whereFilter  int
	havingFilter int
	project      int
	aggregate    int
	window       int
	distinct     int
	sorts        []int
	windowSorts  map[string]int
	limit        int
}

func newSelectRuntimeBinding(metrics *queryexplanation.RuntimeMetrics, plan *relationalSelectPlan) *selectRuntimeBinding {
	if metrics == nil || plan == nil || plan.runtimeKey == "" {
		return nil
	}
	binding := &selectRuntimeBinding{metrics: metrics}
	for index := range plan.source.tables {
		binding.scans = append(binding.scans, runtimeOperatorID(metrics, plan.runtimeKey, "scan", index))
	}
	for index := range plan.source.joins {
		binding.joins = append(binding.joins, runtimeOperatorID(metrics, plan.runtimeKey, "join", len(plan.source.joins)-index-1))
	}
	binding.bindFilters(plan)
	binding.project = runtimeOperatorID(metrics, plan.runtimeKey, "project", 0)
	binding.aggregate = runtimeOperatorID(metrics, plan.runtimeKey, "aggregate", 0)
	binding.window = runtimeOperatorID(metrics, plan.runtimeKey, "window", 0)
	binding.distinct = runtimeOperatorID(metrics, plan.runtimeKey, "distinct", 0)
	binding.bindSorts(plan)
	binding.limit = runtimeOperatorID(metrics, plan.runtimeKey, "limit", 0)
	return binding
}

func (b *selectRuntimeBinding) bindFilters(plan *relationalSelectPlan) {
	filterIndex := 0
	if plan.aggregation.having != "" {
		b.havingFilter = runtimeOperatorID(b.metrics, plan.runtimeKey, "filter", filterIndex)
		filterIndex++
	}
	if plan.where != nil {
		b.whereFilter = runtimeOperatorID(b.metrics, plan.runtimeKey, "filter", filterIndex)
	}
}

func (b *selectRuntimeBinding) bindSorts(plan *relationalSelectPlan) {
	index := 0
	if len(plan.order) > 0 {
		b.sorts = append(b.sorts, runtimeOperatorID(b.metrics, plan.runtimeKey, "sort", index))
		index++
	}
	windowSorts := runtimeWindowSorts(plan)
	if len(windowSorts) == 0 {
		return
	}
	b.windowSorts = make(map[string]int, len(windowSorts))
	for window := len(windowSorts) - 1; window >= 0; window-- {
		b.windowSorts[relationalWindowSpecKey(windowSorts[window])] = runtimeOperatorID(b.metrics, plan.runtimeKey, "sort", index)
		index++
	}
}

func runtimeWindowSorts(plan *relationalSelectPlan) []relationalWindowSpec {
	keys := make(map[string]bool)
	result := make([]relationalWindowSpec, 0)
	for _, projection := range plan.projection {
		for _, function := range projectionWindowFunctions(projection) {
			if len(function.spec.order) == 0 {
				continue
			}
			key := relationalWindowSpecKey(function.spec)
			if keys[key] {
				continue
			}
			keys[key] = true
			result = append(result, function.spec)
		}
	}
	return result
}

func runtimeOperatorID(metrics *queryexplanation.RuntimeMetrics, termKey, kind string, index int) int {
	return metrics.OperatorID(queryexplanation.RuntimeOperatorKey(termKey, kind, index))
}

func reverseOperatorIDs(ids []int) []int {
	result := append([]int(nil), ids...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func finalOperatorID(ids []int) int {
	if len(ids) == 0 {
		return 0
	}
	return ids[len(ids)-1]
}

func (b *selectRuntimeBinding) record(id, inputRows, outputRows, filteredRows, logicalReads, bytesRead, peakMemoryBytes int, elapsed time.Duration) {
	if b == nil {
		return
	}
	b.metrics.RecordOperator(id, inputRows, outputRows, filteredRows, logicalReads, bytesRead, peakMemoryBytes, elapsed)
}

func (b *selectRuntimeBinding) recordScan(index, rows, bytes int, elapsed time.Duration) {
	if b == nil || index >= len(b.scans) {
		return
	}
	b.record(b.scans[index], 0, rows, 0, rows, bytes, 0, elapsed)
}

func (b *selectRuntimeBinding) recordJoin(index, inputRows, outputRows, memory int, elapsed time.Duration) {
	if b == nil || index >= len(b.joins) {
		return
	}
	b.record(b.joins[index], inputRows, outputRows, 0, 0, 0, memory, elapsed)
}

func (b *selectRuntimeBinding) recordMaterialize(key string, rows, bytes int, elapsed time.Duration) {
	if b == nil {
		return
	}
	b.record(b.metrics.OperatorID(key), 0, rows, 0, 0, 0, bytes, elapsed)
}

func (b *selectRuntimeBinding) windowSortID(spec relationalWindowSpec) int {
	if b == nil {
		return 0
	}
	return b.windowSorts[relationalWindowSpecKey(spec)]
}

type relationalTableSource struct {
	namespace      string
	name           string
	alias          string
	table          catalog.Table
	columns        []relationColumn
	query          string
	reason         string
	hints          []relationalIndexHint
	access         *catalog.Index
	forced         bool
	covering       bool
	materializeKey string
}

type relationalIndexHint struct {
	kind    string
	scope   string
	indexes []string
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
	relationalProjectionRuntime
	relationalProjectionWindow
	expression string
	name       string
	alias      string
	column     int
	scalar     bool
	computed   bool
	subquery   string
	context    *composedQueryContext
	value      exprValue
	metadata   columnMetadata
	outer      *outerRelationScope
	aggregate  *relationalAggregate
	window     *relationalWindowFunction
}

type relationalProjectionRuntime struct {
	runtimeKey  string
	subquerySet bool
}

type relationalProjectionWindow struct {
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

type relationRow struct {
	values   []string
	lockKeys []string
}

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
	if s.streamRows && plan.source.locking == nil && !plan.requiresMaterialization() {
		return plan.streamingResult(), nil
	}
	if plan.source.locking != nil {
		return executeLockingRelationalSelect(plan)
	}
	resultRows, err := collectRelationalResultRows(plan)
	if err != nil {
		return nil, err
	}
	return plan.result(plan.shapeRows(resultRows)), nil
}

func executeLockingRelationalSelect(plan *relationalSelectPlan) (*queryResult, error) {
	locks := plan.session.server.locks.snapshot(plan.session)
	resultRows, err := collectRelationalResultRows(plan)
	if err == nil {
		resultRows, err = lockRelationalResultRows(plan, resultRows)
	}
	if err != nil {
		plan.session.server.locks.restore(plan.session, locks)
		return nil, err
	}
	return plan.result(resultRows), nil
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
	started := time.Now()
	sourceRows := 0
	matchedRows := 0
	resultRows := make([]relationalResultRow, 0)
	err := plan.forEachSourceRow(func(row relationRow) error {
		sourceRows++
		if plan.where != nil {
			matched, err := predicateMatches(plan.where, row)
			if err != nil {
				return err
			}
			if !matched {
				return nil
			}
		}
		matchedRows++
		result, err := plan.projectRow(row)
		if err != nil {
			return err
		}
		resultRows = append(resultRows, result)
		return nil
	})
	if err == nil {
		plan.recordReadPipeline(sourceRows, matchedRows, resultRows, time.Since(started))
	}
	return resultRows, err
}

func (p *relationalSelectPlan) recordReadPipeline(sourceRows, matchedRows int, resultRows []relationalResultRow, elapsed time.Duration) {
	if p.runtime == nil {
		return
	}
	if p.where != nil {
		p.runtime.record(p.runtime.whereFilter, sourceRows, matchedRows, sourceRows-matchedRows, 0, 0, 0, elapsed)
	}
	if !p.allColumns {
		p.runtime.record(p.runtime.project, matchedRows, len(resultRows), 0, 0, 0, resultMemory(resultRows), elapsed)
	}
}

func sourceBytes(tables []relationalTableSource) int {
	bytes := 0
	for _, table := range tables {
		for _, row := range table.table.Rows {
			for _, value := range row {
				bytes += len(value)
			}
		}
	}
	return bytes
}

func resultMemory(rows []relationalResultRow) int {
	bytes := 0
	for _, row := range rows {
		bytes += len(row.nulls)
		for _, value := range row.values {
			bytes += len(value)
		}
	}
	return bytes
}

func lockRelationalResultRows(plan *relationalSelectPlan, rows []relationalResultRow) ([]relationalResultRow, error) {
	if plan.source.locking.policy == lockSkip {
		rows = distinctRelationalRows(rows, plan.distinct, plan.projection, plan.source.columns)
		return lockSkippedRelationalRows(plan, sortRelationalRows(rows, plan.order, plan.source.columns))
	}
	return lockReturnedRelationalRows(plan, plan.shapeRows(rows))
}

func lockReturnedRelationalRows(plan *relationalSelectPlan, rows []relationalResultRow) ([]relationalResultRow, error) {
	for _, row := range rows {
		if _, err := plan.source.locking.acquire(plan, row.source); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func lockSkippedRelationalRows(plan *relationalSelectPlan, rows []relationalResultRow) ([]relationalResultRow, error) {
	rows, err := skipLockedOffsetRows(plan, rows)
	if err != nil {
		return nil, err
	}
	return lockSkippedReturnedRows(plan, rows)
}

func skipLockedOffsetRows(plan *relationalSelectPlan, rows []relationalResultRow) ([]relationalResultRow, error) {
	offset := plan.limit.offset
	for index, row := range rows {
		if offset == 0 {
			return rows[index:], nil
		}
		available, err := plan.source.locking.available(plan, row.source)
		if err != nil {
			return nil, err
		}
		if available {
			offset--
		}
	}
	return rows[len(rows):], nil
}

func lockSkippedReturnedRows(plan *relationalSelectPlan, rows []relationalResultRow) ([]relationalResultRow, error) {
	count := len(rows)
	if plan.limit.present && plan.limit.count < count {
		count = plan.limit.count
	}
	locked := make([]relationalResultRow, 0, count)
	for _, row := range rows {
		if len(locked) == count {
			break
		}
		acquired, err := plan.source.locking.acquire(plan, row.source)
		if err != nil {
			return nil, err
		}
		if acquired {
			locked = append(locked, row)
		}
	}
	return locked, nil
}

var errStopRelationStream = errors.New("relational row stream complete")

type relationalResultStream struct {
	plan          *relationalSelectPlan
	skipped       int
	emitted       int
	sourceRows    int
	matchedRows   int
	projectMemory int
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
	started := time.Now()
	defer func() { s.recordRuntime(time.Since(started)) }()
	return s.incrementalRows(yield)
}

func (s *relationalResultStream) recordRuntime(elapsed time.Duration) {
	if s.plan.runtime == nil {
		return
	}
	if s.plan.where != nil {
		s.plan.runtime.record(s.plan.runtime.whereFilter, s.sourceRows, s.matchedRows, s.sourceRows-s.matchedRows, 0, 0, 0, elapsed)
	}
	if !s.plan.allColumns {
		s.plan.runtime.record(s.plan.runtime.project, s.matchedRows, s.emitted, 0, 0, 0, s.projectMemory, elapsed)
	}
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
	s.sourceRows++
	matched, err := relationalRowMatches(s.plan.where, row)
	if err != nil || !matched {
		return err
	}
	s.matchedRows++
	if s.plan.limit.present && s.skipped < s.plan.limit.offset {
		s.skipped++
		return nil
	}
	result, err := s.plan.projectRow(row)
	if err != nil {
		return err
	}
	values, nulls := s.outputRow(result)
	s.emitted++
	s.projectMemory += resultMemory([]relationalResultRow{result})
	if err := yield(values, nulls); err != nil {
		return err
	}
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
	query, locking, err := splitLockingRead(query)
	if err != nil {
		return nil, err
	}
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
	runtimeKey := s.composed.selectRuntimeKey(query)
	source, err := parseRelationalSource(s, sourceText)
	if err != nil {
		return nil, err
	}
	source.locking = locking
	plan := &relationalSelectPlan{relationalSelectEnvironment: relationalSelectEnvironment{session: s.session, composed: s.composed, outer: outer}, distinct: distinct, source: source, whereText: strings.TrimSpace(whereText)}
	plan.runtimeKey = runtimeKey
	if err := finishRelationalSelectPlan(plan, projectionText, groupText, havingText, windowText, orderText, limitText); err != nil {
		return nil, err
	}
	assignSubqueryRuntimeKeys(plan)
	if err := prepareScalarSubqueries(plan); err != nil {
		return nil, err
	}
	plan.runtime = newSelectRuntimeBinding(plan.session.runtimeMetrics, plan)
	plan.source.runtime = plan.runtime
	return plan, nil
}

func assignSubqueryRuntimeKeys(plan *relationalSelectPlan) {
	for index := range plan.projection {
		if plan.projection[index].subquery == "" {
			continue
		}
		plan.projection[index].runtimeKey = queryexplanation.RuntimeOperatorKey(plan.runtimeKey, "subquery", index)
	}
}

func prepareScalarSubqueries(plan *relationalSelectPlan) error {
	if plan.composed == nil || plan.composed.planning {
		return nil
	}
	for index := range plan.projection {
		projection := &plan.projection[index]
		if projection.subquery == "" || !projection.scalar {
			continue
		}
		value, _, err := executeScalarSubquery(projection.context, projection.subquery, nil, projection.runtimeKey)
		if err != nil {
			return err
		}
		projection.value, projection.subquerySet = value, true
	}
	return nil
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
	return plan.chooseIndexAccesses()
}

func (p *relationalSelectPlan) chooseIndexAccesses() error {
	for number := range p.source.tables {
		access, forced, covering, err := chooseTableIndexAccess(p.source.tables[number], p.whereText, p.order, p.projection, p.allColumns, p.source.columns)
		if err != nil {
			return err
		}
		p.source.tables[number].access, p.source.tables[number].forced, p.source.tables[number].covering = access, forced, covering
	}
	for number := range p.source.joins {
		for _, table := range p.source.tables {
			if identifiersEqual(table.alias, p.source.joins[number].right.alias) {
				p.source.joins[number].right.access = table.access
				p.source.joins[number].right.forced = table.forced
				p.source.joins[number].right.covering = table.covering
				break
			}
		}
	}
	return nil
}

func chooseTableIndexAccess(table relationalTableSource, where string, orders []relationalOrder, projections []relationalProjection, allColumns bool, columns []relationColumn) (*catalog.Index, bool, bool, error) {
	indexes := effectiveTableIndexes(table.table)
	allowed, forced, err := indexHintCandidates(table, indexes, indexSelectionScope(where, orders))
	if err != nil {
		return nil, false, false, err
	}
	if forced && len(allowed) > 0 {
		index := allowed[0]
		return cloneIndexPointer(index), true, indexCoversProjection(index, table, projections, allColumns, columns), nil
	}
	for _, index := range allowed {
		if indexSupportsSelect(index, table, where, orders) {
			return cloneIndexPointer(index), false, indexCoversProjection(index, table, projections, allColumns, columns), nil
		}
	}
	return nil, false, false, nil
}

func indexSelectionScope(where string, orders []relationalOrder) string {
	if strings.TrimSpace(where) != "" || len(orders) == 0 {
		return "join"
	}
	return "order_by"
}

func indexCoversProjection(index catalog.Index, table relationalTableSource, projections []relationalProjection, allColumns bool, columns []relationColumn) bool {
	if allColumns {
		return false
	}
	parts := indexProjectionParts(index)
	for _, projection := range projections {
		if !indexCoversProjectionItem(parts, table, projection, columns) {
			return false
		}
	}
	return true
}

func indexProjectionParts(index catalog.Index) map[string]bool {
	parts := map[string]bool{}
	for _, part := range index.Parts {
		addIndexProjectionPart(parts, part)
	}
	return parts
}

func addIndexProjectionPart(parts map[string]bool, part catalog.IndexPart) {
	if part.Column != "" {
		parts["column:"+catalog.Key(part.Column)] = true
	}
	if part.Expression != "" {
		parts["expression:"+normalizedIndexExpression(part.Expression)] = true
	}
}

func indexCoversProjectionItem(parts map[string]bool, table relationalTableSource, projection relationalProjection, columns []relationColumn) bool {
	if projection.scalar || projection.subquery != "" {
		return true
	}
	if projection.computed {
		return parts["expression:"+normalizedIndexExpression(projection.expression)]
	}
	return indexCoversColumnProjection(parts, table, projection.column, columns)
}

func indexCoversColumnProjection(parts map[string]bool, table relationalTableSource, position int, columns []relationColumn) bool {
	if position < 0 || position >= len(columns) {
		return false
	}
	column := columns[position]
	return identifiersEqual(column.qualifier, table.alias) && parts["column:"+catalog.Key(column.name)]
}

func normalizedIndexExpression(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func indexHintCandidates(table relationalTableSource, indexes []catalog.Index, scope string) ([]catalog.Index, bool, error) {
	allowed := visibleTableIndexes(indexes)
	var use, force []string
	ignored := map[string]bool{}
	for _, hint := range table.hints {
		if hint.scope != scope {
			continue
		}
		switch hint.kind {
		case "use":
			use = hint.indexes
		case "force":
			force = hint.indexes
		case "ignore":
			for _, name := range hint.indexes {
				ignored[catalog.Key(name)] = true
			}
		}
	}
	if len(force) > 0 {
		return namedIndexCandidates(indexes, force, ignored), true, nil
	}
	if use != nil {
		return namedIndexCandidates(indexes, use, ignored), false, nil
	}
	return withoutIgnoredIndexes(allowed, ignored), false, nil
}

func visibleTableIndexes(indexes []catalog.Index) []catalog.Index {
	visible := make([]catalog.Index, 0, len(indexes))
	for _, index := range indexes {
		if !index.Invisible {
			visible = append(visible, index)
		}
	}
	return visible
}

func namedIndexCandidates(indexes []catalog.Index, names []string, ignored map[string]bool) []catalog.Index {
	result := make([]catalog.Index, 0, len(names))
	for _, name := range names {
		for _, index := range indexes {
			if catalog.Key(index.Name) == catalog.Key(name) && !ignored[catalog.Key(index.Name)] {
				result = append(result, index)
				break
			}
		}
	}
	return result
}

func withoutIgnoredIndexes(indexes []catalog.Index, ignored map[string]bool) []catalog.Index {
	result := make([]catalog.Index, 0, len(indexes))
	for _, index := range indexes {
		if !ignored[catalog.Key(index.Name)] {
			result = append(result, index)
		}
	}
	return result
}

func indexSupportsSelect(index catalog.Index, table relationalTableSource, where string, orders []relationalOrder) bool {
	if len(index.Parts) == 0 {
		return false
	}
	part := index.Parts[0]
	if indexTextContains(where, table, part) {
		return true
	}
	for _, order := range orders {
		if indexTextContains(order.expression, table, part) {
			return true
		}
	}
	return false
}

func indexTextContains(text string, table relationalTableSource, part catalog.IndexPart) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if part.Expression != "" {
		return strings.Contains(text, strings.ToLower(strings.TrimSpace(part.Expression)))
	}
	column := strings.ToLower(part.Column)
	return strings.Contains(text, column) && (strings.Contains(text, strings.ToLower(table.alias)+"."+column) || strings.Contains(text, column))
}

func cloneIndexPointer(index catalog.Index) *catalog.Index {
	copy := catalog.CloneIndexes([]catalog.Index{index})
	return &copy[0]
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
	runtimeKeys := &predicateRuntimeKeys{prefix: p.runtimeKey}
	if p.whereText != "" {
		predicate, err := compileRelationPredicateContext(p.whereText, p.source.columns, p.session, p.composed, p.outer, runtimeKeys)
		if err != nil {
			return err
		}
		p.where = predicate
	}
	for index := range p.source.joins {
		if p.source.joins[index].condition == "" {
			continue
		}
		predicate, err := compileRelationPredicateContext(p.source.joins[index].condition, p.source.joins[index].columns, p.session, p.composed, p.outer, runtimeKeys)
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
		if at := selectClauseAt(tail, candidate); at >= 0 {
			clauses = append(clauses, struct {
				name string
				at   int
			}{name: candidate, at: at})
		}
	}
	return clauses
}

func selectClauseAt(text, keyword string) int {
	start := 0
	length := len(text)
	for start < length {
		at := keywordAt(text[start:], keyword)
		if at < 0 {
			return -1
		}
		at += start
		if keyword != "order" || !indexHintOrderKeyword(text, at) {
			return at
		}
		start = at + len(keyword)
	}
	return -1
}

func indexHintOrderKeyword(text string, at int) bool {
	before := strings.Fields(strings.ToLower(strings.TrimSpace(text[:at])))
	return len(before) > 0 && before[len(before)-1] == "for"
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
		RuntimeKey:            p.runtimeKey,
		Columns:               projectionNames(p.projection),
		ProjectionExpressions: projectionExpressions(p.projection, p.source.columns),
		AllColumns:            p.allColumns,
		Where:                 p.whereText,
		Aggregation: queryexplanation.Aggregation{
			GroupExpressions: groupExpressions(p.aggregation.groups),
			Having:           p.aggregation.having,
			Count:            aggregateProjectionCount(p.projection),
		},
		Window:   queryexplanation.WindowDetails{Count: windowDefinitionCount(p.projection), FunctionCount: windowProjectionCount(p.projection), Definitions: windowExplanationDefinitions(p.projection)},
		Distinct: p.distinct,
		Orders:   explanationOrders(p.order),
		Limit:    queryexplanation.Limit{Present: p.limit.present, Offset: p.limit.offset, Count: p.limit.count},
		Locking:  queryexplanation.LockingRead{Enabled: p.source.locking != nil},
	}
	if p.source.locking != nil {
		read.Locking.Mode = p.source.locking.explanationMode()
		read.Locking.WaitPolicy = p.source.locking.explanationWaitPolicy()
	}
	for _, table := range p.source.tables {
		read.Tables = append(read.Tables, relationSourceInfo(table))
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
			Type: join.kind, Table: relationSourceInfo(join.right), Condition: join.condition,
			SourceClause: clause, SourceFragment: fragment,
		})
	}
	return queryexplanation.PlanSelect(serverVersion, sql, currentDatabase, read)
}

func relationalRuntimeKey(query string) string {
	return "select:" + strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(query)), " "))
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
