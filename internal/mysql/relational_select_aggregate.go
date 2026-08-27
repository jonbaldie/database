package mysql

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

type relationalGroup struct {
	expression string
	source     string
}

type relationalAggregation struct {
	groups  []relationalGroup
	having  string
	windows map[string]relationalWindowSpec
}

type relationalAggregate struct {
	name     string
	argument string
	distinct bool
}

type relationalWindowFunction struct {
	relationalAggregate
	spec relationalWindowSpec
}

type relationalWindowSpec struct {
	partition []string
	order     []relationalWindowOrder
	frame     relationalWindowFrame
}

type relationalWindowOrder struct {
	expression string
	direction  string
}

type relationalWindowFrame struct {
	present bool
	mode    string
	start   relationalWindowBound
	end     relationalWindowBound
}

type relationalWindowBound struct {
	kind   string
	offset int
}

const mysqlDecimalsNotSpecified byte = 31

var composedWindowScalarTypes = map[byte]string{
	mysqlTypeTiny:   "TINYINT",
	mysqlTypeShort:  "SMALLINT",
	mysqlTypeInt24:  "MEDIUMINT",
	mysqlTypeLong:   "INT",
	mysqlTypeFloat:  "FLOAT",
	mysqlTypeDouble: "DOUBLE",
}

func (p *relationalSelectPlan) compileAggregation(group, having, windows string) error {
	parsedGroup, err := parseRelationalGroups(group, p.source.columns, p.projection, p.outer)
	if err != nil {
		return err
	}
	parsedWindows, err := parseRelationalWindows(windows, p.source.columns, p.outer)
	if err != nil {
		return err
	}
	p.aggregation = relationalAggregation{groups: parsedGroup, having: strings.TrimSpace(having), windows: parsedWindows}
	for index := range p.projection {
		if err := p.resolveProjectionWindows(&p.projection[index]); err != nil {
			return err
		}
	}
	if err := p.validateAggregateProjection(); err != nil {
		return err
	}
	return p.validateGroupedExpression(p.aggregation.having)
}

func (p *relationalSelectPlan) resolveProjectionWindows(projection *relationalProjection) error {
	if len(projection.windowParts) > 0 {
		for index := range projection.windowParts {
			spec, err := resolveRelationalWindow(projection.windowParts[index].function.spec, p.aggregation.windows)
			if err != nil {
				return err
			}
			projection.windowParts[index].function.spec = spec
		}
		projection.window = &projection.windowParts[0].function
		return nil
	}
	if projection.window == nil {
		return nil
	}
	spec, err := resolveRelationalWindow(projection.window.spec, p.aggregation.windows)
	if err != nil {
		return err
	}
	projection.window.spec = spec
	return nil
}

func parseRelationalGroups(text string, columns []relationColumn, projections []relationalProjection, outer *outerRelationScope) ([]relationalGroup, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	groups := make([]relationalGroup, 0, len(splitCSV(text)))
	for _, source := range splitCSV(text) {
		source = strings.TrimSpace(source)
		expression, err := resolveRelationalGroupExpression(source, columns, projections)
		if err != nil {
			return nil, err
		}
		if _, err := evaluateRelationExpressionContext(expression, columns, sampleRelationRow(columns), outer, nil); err != nil {
			return nil, sqlFailure{1054, "42S22", "Unknown column '" + source + "' in 'group statement'"}
		}
		groups = append(groups, relationalGroup{expression: expression, source: source})
	}
	return groups, nil
}

func resolveRelationalGroupExpression(expression string, columns []relationColumn, projections []relationalProjection) (string, error) {
	if ordinal, err := strconv.Atoi(expression); err == nil {
		if ordinal < 1 || ordinal > len(projections) {
			return "", sqlFailure{1054, "42S22", "Unknown column '" + expression + "' in 'group statement'"}
		}
		return groupProjectionExpression(projections[ordinal-1])
	}
	if _, err := resolveRelationColumn(expression, columns); err == nil {
		return expression, nil
	}
	if index, found := projectionIndex(projections, expression); found {
		return groupProjectionExpression(projections[index])
	}
	return expression, nil
}

func groupProjectionExpression(projection relationalProjection) (string, error) {
	if projection.aggregate != nil || projection.window != nil {
		return "", sqlFailure{1055, "42000", "Cannot group on an aggregate or window expression"}
	}
	return projection.expression, nil
}

func parseRelationalWindows(text string, columns []relationColumn, outer *outerRelationScope) (map[string]relationalWindowSpec, error) {
	if strings.TrimSpace(text) == "" {
		return map[string]relationalWindowSpec{}, nil
	}
	result := make(map[string]relationalWindowSpec)
	for _, item := range splitCSV(text) {
		found, name, definition := splitRelationKeywordOnce(item, "AS")
		if !found {
			return nil, sqlFailure{1064, "42000", "invalid WINDOW definition"}
		}
		identifier, valid := singleIdentifier(name)
		key := catalog.Key(identifier)
		if _, exists := result[key]; !valid || exists {
			return nil, sqlFailure{1064, "42000", "invalid WINDOW name"}
		}
		spec, err := parseRelationalWindowSpec(definition, columns, outer)
		if err != nil {
			return nil, err
		}
		result[key] = spec
	}
	return result, nil
}

func parseRelationalWindowSpec(text string, columns []relationColumn, outer *outerRelationScope) (relationalWindowSpec, error) {
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != '(' || text[len(text)-1] != ')' {
		return relationalWindowSpec{}, sqlFailure{1064, "42000", "invalid window specification"}
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	sections := windowSectionsFrom(body)
	spec, err := parseRelationalWindowClauses(body, sections, columns, outer)
	if err != nil {
		return relationalWindowSpec{}, err
	}
	if !sections.hasClause() && body != "" {
		return relationalWindowSpec{}, sqlFailure{1064, "42000", "unsupported window frame"}
	}
	return spec, nil
}

type relationalWindowSections struct {
	partition int
	order     int
	frame     int
	frameMode string
}

func windowSectionsFrom(body string) relationalWindowSections {
	frame, mode := relationalWindowFrameAt(body)
	return relationalWindowSections{partition: keywordAt(body, "partition"), order: keywordAt(body, "order"), frame: frame, frameMode: mode}
}

func (s relationalWindowSections) hasClause() bool {
	return s.partition >= 0 || s.order >= 0 || s.frame >= 0
}

func parseRelationalWindowClauses(body string, sections relationalWindowSections, columns []relationColumn, outer *outerRelationScope) (relationalWindowSpec, error) {
	partition, err := parseRelationalWindowPartition(body, sections, columns, outer)
	if err != nil {
		return relationalWindowSpec{}, err
	}
	order, err := parseRelationalWindowOrder(body, sections, columns, outer)
	if err != nil {
		return relationalWindowSpec{}, err
	}
	frame, err := parseRelationalWindowFrameClause(body, sections)
	if err != nil {
		return relationalWindowSpec{}, err
	}
	if frame.mode == "range" && rangeFrameHasOffset(frame) && len(order) == 0 {
		return relationalWindowSpec{}, sqlFailure{3587, "HY000", "RANGE frame with value offset requires one ORDER BY expression"}
	}
	return relationalWindowSpec{partition: partition, order: order, frame: frame}, nil
}

func parseRelationalWindowPartition(body string, sections relationalWindowSections, columns []relationColumn, outer *outerRelationScope) ([]string, error) {
	if sections.partition < 0 {
		return nil, nil
	}
	partition := windowClauseBody(body, sections.partition, len("partition"), sections.order, sections.frame)
	if !strings.HasPrefix(strings.ToLower(partition), "by ") {
		return nil, sqlFailure{1064, "42000", "PARTITION requires BY"}
	}
	return checkedWindowExpressions(strings.TrimSpace(partition[len("by "):]), columns, outer)
}

func parseRelationalWindowOrder(body string, sections relationalWindowSections, columns []relationColumn, outer *outerRelationScope) ([]relationalWindowOrder, error) {
	if sections.order < 0 {
		return nil, nil
	}
	order := windowClauseBody(body, sections.order, len("order"), sections.frame)
	if !strings.HasPrefix(strings.ToLower(order), "by ") {
		return nil, sqlFailure{1064, "42000", "ORDER requires BY"}
	}
	return checkedWindowOrder(strings.TrimSpace(order[len("by "):]), columns, outer)
}

func windowClauseBody(body string, at, prefix int, next ...int) string {
	end := len(body)
	for _, candidate := range next {
		if candidate > at && candidate < end {
			end = candidate
		}
	}
	return strings.TrimSpace(body[at+prefix : end])
}

func checkedWindowExpressions(text string, columns []relationColumn, outer *outerRelationScope) ([]string, error) {
	result := make([]string, 0, len(splitCSV(text)))
	for _, expression := range splitCSV(text) {
		expression = strings.TrimSpace(expression)
		if _, err := evaluateRelationExpressionContext(expression, columns, sampleRelationRow(columns), outer, nil); err != nil {
			return nil, err
		}
		result = append(result, expression)
	}
	return result, nil
}

func checkedWindowOrder(text string, columns []relationColumn, outer *outerRelationScope) ([]relationalWindowOrder, error) {
	result := make([]relationalWindowOrder, 0, len(splitCSV(text)))
	for _, item := range splitCSV(text) {
		expression, direction := splitOrderDirection(item)
		if isAggregateExpression(expression) {
			result = append(result, relationalWindowOrder{expression: expression, direction: direction})
			continue
		}
		if _, err := evaluateRelationExpressionContext(expression, columns, sampleRelationRow(columns), outer, nil); err != nil {
			return nil, err
		}
		result = append(result, relationalWindowOrder{expression: expression, direction: direction})
	}
	return result, nil
}

func isAggregateExpression(expression string) bool {
	function, tail, found := relationalFunction(expression)
	if !found || strings.TrimSpace(tail) != "" || !isAggregateName(strings.ToUpper(function.name)) {
		return false
	}
	_, err := parseRelationalAggregate(strings.ToUpper(function.name), function.arguments)
	return err == nil
}

func parseRelationalWindowFrameClause(body string, sections relationalWindowSections) (relationalWindowFrame, error) {
	if sections.frame < 0 {
		return relationalWindowFrame{}, nil
	}
	return parseRelationalWindowFrame(strings.TrimSpace(body[sections.frame:]), sections.frameMode)
}

func relationalWindowFrameAt(text string) (int, string) {
	rows, valueRange := keywordAt(text, "rows"), keywordAt(text, "range")
	switch {
	case rows >= 0 && (valueRange < 0 || rows < valueRange):
		return rows, "rows"
	case valueRange >= 0:
		return valueRange, "range"
	default:
		return -1, ""
	}
}

func parseRelationalWindowFrame(text, mode string) (relationalWindowFrame, error) {
	body := strings.TrimSpace(text[len(mode):])
	if body == "" {
		return relationalWindowFrame{}, sqlFailure{1064, "42000", "invalid window frame"}
	}
	startText, endText := body, "CURRENT ROW"
	if strings.HasPrefix(strings.ToLower(body), "between ") {
		between := strings.TrimSpace(body[len("between "):])
		found, start, end := splitRelationKeywordOnce(between, "AND")
		if !found {
			return relationalWindowFrame{}, sqlFailure{1064, "42000", "invalid window frame"}
		}
		startText, endText = start, end
	}
	start, err := parseRelationalWindowBound(startText)
	if err != nil {
		return relationalWindowFrame{}, err
	}
	end, err := parseRelationalWindowBound(endText)
	if err != nil {
		return relationalWindowFrame{}, err
	}
	if start.kind == "unbounded_following" || end.kind == "unbounded_preceding" {
		return relationalWindowFrame{}, sqlFailure{1064, "42000", "invalid window frame"}
	}
	if !windowFrameBoundsInOrder(start, end) {
		return relationalWindowFrame{}, sqlFailure{1064, "42000", "invalid window frame"}
	}
	return relationalWindowFrame{present: true, mode: mode, start: start, end: end}, nil
}

func windowFrameBoundsInOrder(start, end relationalWindowBound) bool {
	startClass, startOffset := windowFrameBoundOrder(start)
	endClass, endOffset := windowFrameBoundOrder(end)
	if startClass != endClass {
		return startClass < endClass
	}
	if startClass == 1 {
		return startOffset >= endOffset
	}
	return startOffset <= endOffset
}

func windowFrameBoundOrder(bound relationalWindowBound) (int, int) {
	switch bound.kind {
	case "unbounded_preceding":
		return 0, 0
	case "preceding":
		return 1, bound.offset
	case "current_row":
		return 2, 0
	case "following":
		return 3, bound.offset
	case "unbounded_following":
		return 4, 0
	default:
		return 5, 0
	}
}

func parseRelationalWindowBound(text string) (relationalWindowBound, error) {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if isCurrentRowBound(words) {
		return relationalWindowBound{kind: "current_row"}, nil
	}
	if direction, ok := unboundedWindowBound(words); ok {
		return relationalWindowBound{kind: "unbounded_" + direction}, nil
	}
	return relativeWindowBound(words)
}

func isCurrentRowBound(words []string) bool {
	return len(words) == 2 && words[0] == "current" && words[1] == "row"
}

func unboundedWindowBound(words []string) (string, bool) {
	if len(words) != 2 || words[0] != "unbounded" {
		return "", false
	}
	return windowBoundDirection(words[1])
}

func relativeWindowBound(words []string) (relationalWindowBound, error) {
	if len(words) != 2 {
		return relationalWindowBound{}, sqlFailure{1064, "42000", "invalid window frame bound"}
	}
	direction, valid := windowBoundDirection(words[1])
	if !valid {
		return relationalWindowBound{}, sqlFailure{1064, "42000", "invalid window frame bound"}
	}
	value, err := evaluateScalar(words[0])
	if err != nil || value.kind != valueInt || value.i < 0 || value.i > int64(maxIntValue()) {
		return relationalWindowBound{}, sqlFailure{1064, "42000", "invalid window frame bound"}
	}
	return relationalWindowBound{kind: direction, offset: int(value.i)}, nil
}

func windowBoundDirection(value string) (string, bool) {
	return value, value == "preceding" || value == "following"
}

func resolveRelationalWindow(spec relationalWindowSpec, named map[string]relationalWindowSpec) (relationalWindowSpec, error) {
	if len(spec.partition) != 1 || !strings.HasPrefix(spec.partition[0], "@") {
		return spec, nil
	}
	name := strings.TrimPrefix(spec.partition[0], "@")
	resolved, found := named[catalog.Key(name)]
	if !found {
		return relationalWindowSpec{}, sqlFailure{1054, "42S22", "Unknown window '" + name + "'"}
	}
	return resolved, nil
}

func (p *relationalSelectPlan) validateAggregateProjection() error {
	if !p.requiresGrouping() {
		return nil
	}
	for _, projection := range p.projection {
		if projection.aggregate != nil || projection.window != nil || projection.scalar {
			continue
		}
		if !projectionMatchesGroup(projection, p.aggregation.groups) {
			return sqlFailure{1055, "42000", "Expression is not in GROUP BY clause and contains nonaggregated column"}
		}
	}
	return nil
}

func (p *relationalSelectPlan) validateGroupedOrders(orders []relationalOrder) error {
	if !p.requiresGrouping() {
		return nil
	}
	for _, order := range orders {
		if order.fromProjection {
			continue
		}
		if err := p.validateGroupedExpression(order.expression); err != nil {
			return err
		}
	}
	return nil
}

func (p *relationalSelectPlan) validateGroupedExpression(expression string) error {
	if !p.requiresGrouping() || strings.TrimSpace(expression) == "" {
		return nil
	}
	replaced, err := p.replaceGroupAggregates(expression, nil)
	if err != nil {
		return err
	}
	_, err = evaluateScalarWithResolver(replaced, func(name string) (exprValue, error) {
		if index, found := projectionIndex(p.projection, name); found {
			return p.projection[index].value, nil
		}
		if !groupExpressionMatches(name, p.aggregation.groups) {
			return exprValue{}, sqlFailure{1055, "42000", "Expression is not in GROUP BY clause and contains nonaggregated column"}
		}
		return evaluateRelationExpressionContext(name, p.source.columns, sampleRelationRow(p.source.columns), p.outer, p.session)
	})
	return err
}

func (p *relationalSelectPlan) requiresGrouping() bool {
	return p.hasAggregate() || len(p.aggregation.groups) > 0 || p.aggregation.having != ""
}

func projectionMatchesGroup(projection relationalProjection, groups []relationalGroup) bool {
	return groupExpressionMatches(projection.expression, groups)
}

func groupExpressionMatches(expression string, groups []relationalGroup) bool {
	for _, group := range groups {
		if strings.EqualFold(strings.TrimSpace(expression), strings.TrimSpace(group.expression)) {
			return true
		}
	}
	return false
}

func (p *relationalSelectPlan) hasAggregateOrWindow() bool {
	for _, projection := range p.projection {
		if projection.aggregate != nil || projection.window != nil {
			return true
		}
	}
	return len(p.aggregation.groups) > 0 || p.aggregation.having != ""
}

func parseAggregateProjection(expression, alias string, columns []relationColumn) ([]relationalProjection, bool, error) {
	function, tail, found := relationalFunction(expression)
	if !found {
		return nil, false, nil
	}
	name := strings.ToUpper(function.name)
	if !isAggregateName(name) {
		return nil, false, nil
	}
	projection, err := aggregateProjection(expression, alias, columns, name, function.arguments)
	if err != nil {
		return nil, true, err
	}
	return projectAggregateWindow(projection, tail, columns)
}

func aggregateProjection(expression, alias string, columns []relationColumn, name, arguments string) (relationalProjection, error) {
	aggregate, err := parseRelationalAggregate(name, arguments)
	if err != nil {
		return relationalProjection{}, err
	}
	metadata, err := aggregateMetadata(aggregate, columns)
	if err != nil {
		return relationalProjection{}, err
	}
	projectionName := expression
	if alias != "" {
		projectionName = alias
	}
	metadata.name = projectionName
	return relationalProjection{expression: expression, name: projectionName, alias: alias, column: -1, metadata: metadata, aggregate: &aggregate}, nil
}

func projectAggregateWindow(projection relationalProjection, tail string, columns []relationColumn) ([]relationalProjection, bool, error) {
	if strings.EqualFold(strings.TrimSpace(tail), "") {
		return []relationalProjection{projection}, true, nil
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(tail)), "over ") {
		return nil, true, unsupportedExpression()
	}
	if projection.aggregate.distinct {
		return nil, true, sqlFailure{1235, "42000", "DISTINCT is not supported for window functions"}
	}
	specification := strings.TrimSpace(strings.TrimSpace(tail)[len("over "):])
	spec, err := parseWindowReference(specification, columns)
	if err != nil {
		return nil, true, err
	}
	projection.aggregate, projection.window = nil, &relationalWindowFunction{relationalAggregate: *projection.aggregate, spec: spec}
	return []relationalProjection{projection}, true, nil
}

func parseWindowProjection(expression, alias string, columns []relationColumn) ([]relationalProjection, bool, error) {
	function, specification, found := directWindowFunction(expression)
	name := strings.ToUpper(function.name)
	if !found || !isWindowName(name) {
		return nil, false, nil
	}
	spec, err := parseWindowReference(specification, columns)
	if err != nil {
		return nil, true, err
	}
	aggregate, err := parseRelationalAggregate(name, function.arguments)
	if err != nil {
		return nil, true, err
	}
	metadata, err := aggregateMetadata(aggregate, columns)
	if err != nil {
		return nil, true, err
	}
	projectionName := expression
	if alias != "" {
		projectionName = alias
	}
	metadata.name = projectionName
	return []relationalProjection{{expression: expression, name: projectionName, alias: alias, column: -1, metadata: metadata, window: &relationalWindowFunction{relationalAggregate: aggregate, spec: spec}}}, true, nil
}

func directWindowFunction(expression string) (relationalFunctionCall, string, bool) {
	function, tail, found := relationalFunction(expression)
	if !found {
		return relationalFunctionCall{}, "", false
	}
	specification, found := directWindowSpecificationTail(tail)
	return function, specification, found
}

func directWindowSpecificationTail(tail string) (string, bool) {
	tail = strings.TrimSpace(tail)
	if !strings.HasPrefix(strings.ToLower(tail), "over ") {
		return "", false
	}
	specification := strings.TrimSpace(tail[len("over "):])
	return specification, directWindowSpecification(specification)
}

func directWindowSpecification(specification string) bool {
	if specification == "" {
		return false
	}
	if specification[0] != '(' {
		_, remainder, valid := consumeIdentifier(specification)
		return valid && strings.TrimSpace(remainder) == ""
	}
	end, found := matchingParenthesis(specification, 0)
	return found && end == len(specification)-1
}

func parseComposedWindowProjection(expression, alias string, columns []relationColumn) ([]relationalProjection, bool, error) {
	if composedWindowIsWholeExpression(expression) {
		return nil, false, nil
	}
	windowExpression, windows, found, err := composedWindowParts(expression, columns)
	if !found {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	metadata, err := composedWindowMetadata(windowExpression, windows, columns)
	if err != nil {
		return nil, true, err
	}
	projectionName := expression
	if alias != "" {
		projectionName = alias
	}
	metadata.name = projectionName
	window := &windows[0].function
	return []relationalProjection{{
		expression: expression, name: projectionName, alias: alias, column: -1, computed: true,
		metadata: metadata, window: window,
		relationalProjectionWindow: relationalProjectionWindow{windowExpr: windowExpression, windowParts: windows},
	}}, true, nil
}

func composedWindowIsWholeExpression(expression string) bool {
	start, end, _, _, found := composedWindowFunction(expression, 0)
	return found && start == 0 && end == len(expression)
}

func composedWindowParts(expression string, columns []relationColumn) (string, []relationalComposedWindow, bool, error) {
	var builder strings.Builder
	windows := make([]relationalComposedWindow, 0)
	used := make(map[string]struct{})
	length, start, copied := len(expression), 0, 0
	for start < length {
		callStart, callEnd, function, specification, found := composedWindowFunction(expression, start)
		if !found {
			break
		}
		window, err := composedWindowPart(function, specification, columns, used)
		if err != nil {
			return "", nil, true, err
		}
		builder.WriteString(expression[copied:callStart])
		builder.WriteString(window.placeholder)
		windows = append(windows, window)
		start, copied = callEnd, callEnd
	}
	if len(windows) == 0 {
		return "", nil, false, nil
	}
	builder.WriteString(expression[copied:])
	return builder.String(), windows, true, nil
}

func composedWindowPart(function relationalFunctionCall, specification string, columns []relationColumn, used map[string]struct{}) (relationalComposedWindow, error) {
	spec, err := parseWindowReference(specification, columns)
	if err != nil {
		return relationalComposedWindow{}, err
	}
	aggregate, err := parseRelationalAggregate(strings.ToUpper(function.name), function.arguments)
	if err != nil {
		return relationalComposedWindow{}, err
	}
	if aggregate.distinct {
		return relationalComposedWindow{}, sqlFailure{1235, "42000", "DISTINCT is not supported for window functions"}
	}
	metadata, err := aggregateMetadata(aggregate, columns)
	if err != nil {
		return relationalComposedWindow{}, err
	}
	placeholder := composedWindowPlaceholder(columns, used)
	used[placeholder] = struct{}{}
	return relationalComposedWindow{function: relationalWindowFunction{relationalAggregate: aggregate, spec: spec}, placeholder: placeholder, metadata: metadata}, nil
}

func composedWindowFunction(expression string, start int) (int, int, relationalFunctionCall, string, bool) {
	length := len(expression)
	for start < length {
		candidate, found := nextComposedWindowCandidate(expression, start)
		if !found {
			break
		}
		function, callEnd, valid := composedWindowCall(expression, candidate)
		if valid {
			specificationEnd, specification, hasSpecification := composedWindowSpecification(expression, callEnd)
			if hasSpecification {
				return candidate, specificationEnd, function, specification, true
			}
		}
		start = candidate + 1
	}
	return 0, 0, relationalFunctionCall{}, "", false
}

func nextComposedWindowCandidate(expression string, start int) (int, bool) {
	length := len(expression)
	for start < length {
		if !isAggregateIdentifierByte(expression[start]) {
			start++
			continue
		}
		end := composedWindowIdentifierEnd(expression, start)
		name := strings.ToUpper(expression[start:end])
		if isWindowName(name) || isAggregateName(name) {
			return start, true
		}
		start = end
	}
	return 0, false
}

func composedWindowCall(expression string, start int) (relationalFunctionCall, int, bool) {
	identifierEnd := composedWindowIdentifierEnd(expression, start)
	open := composedWindowSpaceEnd(expression, identifierEnd)
	if open >= len(expression) || expression[open] != '(' {
		return relationalFunctionCall{}, 0, false
	}
	close, found := matchingParenthesis(expression, open)
	if !found {
		return relationalFunctionCall{}, 0, false
	}
	function, _, found := relationalFunction(expression[start : close+1])
	return function, close + 1, found
}

func composedWindowSpecification(expression string, start int) (int, string, bool) {
	over := composedWindowSpaceEnd(expression, start)
	if !composedWindowOverAt(expression, over) {
		return 0, "", false
	}
	start = composedWindowSpaceEnd(expression, over+4)
	if start >= len(expression) {
		return 0, "", false
	}
	if expression[start] == '(' {
		end, found := matchingParenthesis(expression, start)
		if !found {
			return 0, "", false
		}
		return end + 1, expression[start : end+1], true
	}
	end := composedWindowIdentifierEnd(expression, start)
	if end == start {
		return 0, "", false
	}
	return end, expression[start:end], true
}

func composedWindowIdentifierEnd(expression string, start int) int {
	length := len(expression)
	for start < length && isAggregateIdentifierByte(expression[start]) {
		start++
	}
	return start
}

func composedWindowSpaceEnd(expression string, start int) int {
	length := len(expression)
	for start < length && (expression[start] == ' ' || expression[start] == '\t' || expression[start] == '\n') {
		start++
	}
	return start
}

func composedWindowOverAt(expression string, start int) bool {
	end := start + len("over")
	return end <= len(expression) && strings.EqualFold(expression[start:end], "over") && (end == len(expression) || !isAggregateIdentifierByte(expression[end]))
}

func composedWindowPlaceholder(columns []relationColumn, used map[string]struct{}) string {
	for index := 0; ; index++ {
		placeholder := "__database_window_value_" + strconv.Itoa(index)
		if _, found := used[placeholder]; found {
			continue
		}
		if _, err := resolveRelationColumn(placeholder, columns); err != nil {
			return placeholder
		}
	}
}

func composedWindowMetadata(expression string, windows []relationalComposedWindow, columns []relationColumn) (columnMetadata, error) {
	columns = append([]relationColumn(nil), columns...)
	for _, window := range windows {
		columns = append(columns, relationColumn{name: window.placeholder, qualifier: window.placeholder, index: len(columns), typeName: composedWindowTypeName(window.metadata), metadata: window.metadata})
	}
	return relationExpressionMetadata(expression, columns)
}

func composedWindowTypeName(metadata columnMetadata) string {
	if typeName, found := composedWindowScalarTypes[metadata.typ]; found {
		return typeName
	}
	if metadata.typ == mysqlTypeLongLong {
		return composedWindowBigIntTypeName(metadata)
	}
	if metadata.typ == mysqlTypeNewDecimal {
		precision := int(metadata.length) - 1
		if metadata.decimals > 0 {
			precision--
		}
		return "DECIMAL(" + strconv.Itoa(precision) + "," + strconv.Itoa(int(metadata.decimals)) + ")"
	}
	return "VARCHAR(1)"
}

func composedWindowBigIntTypeName(metadata columnMetadata) string {
	if metadata.flags&mysqlUnsignedFlag != 0 {
		return "BIGINT UNSIGNED"
	}
	return "BIGINT"
}

type relationalFunctionCall struct {
	name      string
	arguments string
}

func relationalFunction(text string) (relationalFunctionCall, string, bool) {
	text = strings.TrimSpace(text)
	open := strings.IndexByte(text, '(')
	if open < 1 {
		return relationalFunctionCall{}, "", false
	}
	name, valid := singleIdentifier(strings.TrimSpace(text[:open]))
	close, found := matchingParenthesis(text, open)
	if !valid || !found {
		return relationalFunctionCall{}, "", false
	}
	return relationalFunctionCall{name: name, arguments: strings.TrimSpace(text[open+1 : close])}, strings.TrimSpace(text[close+1:]), true
}

func parseWindowReference(text string, columns []relationColumn) (relationalWindowSpec, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "(") {
		return parseRelationalWindowSpec(text, columns, nil)
	}
	if name, valid := singleIdentifier(text); valid {
		return relationalWindowSpec{partition: []string{"@" + name}}, nil
	}
	return parseRelationalWindowSpec(text, columns, nil)
}

func isAggregateName(name string) bool {
	return name == "COUNT" || name == "SUM" || name == "AVG" || name == "MIN" || name == "MAX"
}

func isWindowName(name string) bool {
	return name == "ROW_NUMBER" || name == "RANK" || name == "DENSE_RANK" || name == "LAG" || name == "LEAD" || name == "FIRST_VALUE" || name == "LAST_VALUE" || name == "NTH_VALUE"
}

func parseRelationalAggregate(name, arguments string) (relationalAggregate, error) {
	arguments = strings.TrimSpace(arguments)
	if isWindowName(name) {
		return parseWindowFunction(name, arguments)
	}
	return parseAggregateFunction(name, arguments)
}

func parseWindowFunction(name, arguments string) (relationalAggregate, error) {
	withoutArguments := name == "ROW_NUMBER" || name == "RANK" || name == "DENSE_RANK"
	if withoutArguments && arguments != "" {
		return relationalAggregate{}, sqlFailure{1064, "42000", "invalid window arguments"}
	}
	if !withoutArguments && arguments == "" {
		return relationalAggregate{}, sqlFailure{1064, "42000", "invalid window arguments"}
	}
	return relationalAggregate{name: name, argument: arguments}, nil
}

func parseAggregateFunction(name, arguments string) (relationalAggregate, error) {
	distinct, arguments := splitAggregateDistinct(arguments)
	if name == "COUNT" && arguments == "*" {
		if distinct {
			return relationalAggregate{}, sqlFailure{1064, "42000", "invalid aggregate arguments"}
		}
		return relationalAggregate{name: name, argument: "*", distinct: distinct}, nil
	}
	if aggregateArgumentsInvalid(name, arguments, distinct) {
		return relationalAggregate{}, sqlFailure{1064, "42000", "invalid aggregate arguments"}
	}
	return relationalAggregate{name: name, argument: arguments, distinct: distinct}, nil
}

func splitAggregateDistinct(arguments string) (bool, string) {
	if strings.HasPrefix(strings.ToLower(arguments), "distinct ") {
		return true, strings.TrimSpace(arguments[len("distinct "):])
	}
	return false, arguments
}

func aggregateArgumentsInvalid(name, arguments string, distinct bool) bool {
	if arguments == "" || (name != "COUNT" && arguments == "*") {
		return true
	}
	if name != "COUNT" {
		return len(splitCSV(arguments)) != 1
	}
	return distinct && aggregateDistinctHasEmptyArgument(arguments)
}

func aggregateDistinctHasEmptyArgument(arguments string) bool {
	for _, argument := range splitCSV(arguments) {
		if strings.TrimSpace(argument) == "" {
			return true
		}
	}
	return false
}

func aggregateMetadata(aggregate relationalAggregate, columns []relationColumn) (columnMetadata, error) {
	switch aggregate.name {
	case "COUNT", "ROW_NUMBER", "RANK", "DENSE_RANK":
		return aggregateUnsignedIntegerMetadata(), nil
	case "SUM", "AVG":
		return aggregateNumericMetadata(aggregate, columns)
	default:
		if aggregate.argument == "*" {
			return scalarMetadata("", "0", intValue(0)), nil
		}
		argument := aggregate.argument
		if isWindowName(aggregate.name) {
			argument = splitCSV(argument)[0]
		}
		metadata, err := aggregateArgumentMetadata(argument, columns)
		if err != nil {
			return columnMetadata{}, err
		}
		return aggregateWindowOffsetMetadata(aggregate, metadata, columns)
	}
}

func aggregateWindowOffsetMetadata(aggregate relationalAggregate, metadata columnMetadata, columns []relationColumn) (columnMetadata, error) {
	if aggregate.name != "LAG" && aggregate.name != "LEAD" {
		return metadata, nil
	}
	arguments := splitCSV(aggregate.argument)
	if len(arguments) != 3 {
		return metadata, nil
	}
	valueMetadata, err := aggregateWindowArgumentMetadata(arguments[0], columns)
	if err != nil {
		return columnMetadata{}, err
	}
	defaultMetadata, err := aggregateWindowArgumentMetadata(arguments[2], columns)
	if err != nil {
		return columnMetadata{}, err
	}
	return aggregateOffsetMetadata(valueMetadata, defaultMetadata), nil
}

func aggregateWindowArgumentMetadata(argument string, columns []relationColumn) (columnMetadata, error) {
	metadata, err := aggregateArgumentMetadata(argument, columns)
	if err != nil {
		return columnMetadata{}, err
	}
	if value, valueErr := evaluateScalar(argument); valueErr == nil && !value.isNull() {
		metadata.flags |= mysqlNotNullFlag
	}
	return metadata, nil
}

func aggregateOffsetMetadata(value, fallback columnMetadata) columnMetadata {
	if isNumericWireType(value.typ) && isNumericWireType(fallback.typ) {
		return aggregateOffsetNumericMetadata(value, fallback)
	}
	if isCharacterWireType(value.typ) || isCharacterWireType(fallback.typ) {
		return aggregateOffsetCharacterMetadata(value, fallback)
	}
	return value
}

func aggregateOffsetNumericMetadata(value, fallback columnMetadata) columnMetadata {
	result := value
	result.length, result.decimals = max(value.length, fallback.length), max(value.decimals, fallback.decimals)
	result.flags &^= mysqlUnsignedFlag
	if isApproximateWireType(value.typ) || isApproximateWireType(fallback.typ) {
		result.typ, result.length, result.decimals = mysqlTypeDouble, 22, mysqlDecimalsNotSpecified
		return result
	}
	if value.typ == mysqlTypeNewDecimal || fallback.typ == mysqlTypeNewDecimal {
		result.typ = mysqlTypeNewDecimal
		return result
	}
	result.typ, result.length, result.decimals = mysqlTypeLongLong, 21, 0
	return result
}

func aggregateOffsetCharacterMetadata(value, fallback columnMetadata) columnMetadata {
	if isCharacterWireType(fallback.typ) {
		value = fallback
	}
	value.length = max(value.length, fallback.length)
	if value.flags&mysqlNotNullFlag == 0 || fallback.flags&mysqlNotNullFlag == 0 {
		value.flags &^= mysqlNotNullFlag
	}
	return value
}

func aggregateUnsignedIntegerMetadata() columnMetadata {
	metadata := scalarMetadata("", "0", uintValue(0))
	metadata.length = 21
	return metadata
}

func aggregateNumericMetadata(aggregate relationalAggregate, columns []relationColumn) (columnMetadata, error) {
	argument := aggregate.argument
	if isWindowName(aggregate.name) {
		argument = splitCSV(argument)[0]
	}
	metadata, err := aggregateArgumentMetadata(argument, columns)
	if err != nil {
		return columnMetadata{}, err
	}
	metadata.flags &^= mysqlNotNullFlag | mysqlUnsignedFlag
	if metadata.typ == mysqlTypeFloat || metadata.typ == mysqlTypeDouble {
		metadata.typ, metadata.length, metadata.decimals = mysqlTypeDouble, 22, mysqlDecimalsNotSpecified
		return metadata, nil
	}
	precision, scale := aggregateDecimalPrecision(argument, columns, metadata)
	if aggregate.name == "SUM" {
		precision = min(decimalPrecisionCeiling, precision+19)
	}
	if aggregate.name == "AVG" {
		precision, scale = min(decimalPrecisionCeiling, precision+4), min(30, scale+4)
	}
	metadata.typ, metadata.length, metadata.decimals = mysqlTypeNewDecimal, aggregateDecimalLength(precision, scale), byte(scale)
	return metadata, nil
}

func aggregateDecimalPrecision(argument string, columns []relationColumn, metadata columnMetadata) (int, int) {
	if precision, scale, found := aggregateColumnPrecision(argument, columns); found {
		return precision, scale
	}
	return aggregateMetadataDecimalPrecision(metadata)
}

func aggregateColumnPrecision(argument string, columns []relationColumn) (int, int, bool) {
	index, err := resolveRelationColumn(argument, columns)
	if err != nil {
		return 0, 0, false
	}
	numeric, err := parseNumericType(columns[index].typeName)
	if err != nil {
		return 0, 0, false
	}
	switch numeric.kind {
	case numericDecimal:
		return numeric.precision, numeric.scale, true
	case numericInteger:
		return aggregateIntegerPrecision(numeric), 0, true
	case numericBoolean:
		return 1, 0, true
	case numericBit:
		return numeric.width, 0, true
	default:
		return 0, 0, false
	}
}

func aggregateMetadataDecimalPrecision(metadata columnMetadata) (int, int) {
	if metadata.typ == mysqlTypeNewDecimal {
		precision := int(metadata.length) - 1
		if metadata.decimals > 0 {
			precision--
		}
		if precision < 1 {
			precision = 1
		}
		return precision, int(metadata.decimals)
	}
	return aggregateMetadataPrecision(metadata), int(metadata.decimals)
}

func aggregateIntegerPrecision(numeric numericType) int {
	if numeric.unsigned {
		return len(strconv.FormatUint(numeric.umax, 10))
	}
	return len(strconv.FormatInt(numeric.smax, 10))
}

func aggregateMetadataPrecision(metadata columnMetadata) int {
	switch metadata.typ {
	case mysqlTypeTiny:
		return 3
	case mysqlTypeShort:
		return 5
	case mysqlTypeInt24:
		return 8
	case mysqlTypeLong:
		return 10
	case mysqlTypeLongLong:
		if metadata.flags&mysqlUnsignedFlag != 0 {
			return 20
		}
		return 19
	default:
		return min(decimalPrecisionCeiling, max(1, int(metadata.length)))
	}
}

func aggregateDecimalLength(precision, scale int) uint32 {
	length := precision + 1
	if scale > 0 {
		length++
	}
	return uint32(length)
}

func aggregateArgumentMetadata(argument string, columns []relationColumn) (columnMetadata, error) {
	if function, tail, found := relationalFunction(argument); found && tail == "" && isAggregateName(strings.ToUpper(function.name)) {
		nested, err := parseAggregateFunction(strings.ToUpper(function.name), function.arguments)
		if err != nil {
			return columnMetadata{}, err
		}
		return nullableAggregateMetadata(nested, columns)
	}
	metadata, err := relationExpressionMetadata(argument, columns)
	if err != nil {
		return columnMetadata{}, err
	}
	metadata.flags &^= mysqlNotNullFlag
	return metadata, nil
}

func nullableAggregateMetadata(aggregate relationalAggregate, columns []relationColumn) (columnMetadata, error) {
	metadata, err := aggregateMetadata(aggregate, columns)
	if err != nil {
		return columnMetadata{}, err
	}
	metadata.flags &^= mysqlNotNullFlag
	return metadata, nil
}

func collectAggregateOrWindowRows(plan *relationalSelectPlan) ([]relationalResultRow, error) {
	sourceRows, err := collectFilteredSourceRows(plan)
	if err != nil {
		return nil, err
	}
	if plan.hasAggregate() || len(plan.aggregation.groups) > 0 || plan.aggregation.having != "" {
		rows, err := collectGroupedRows(plan, sourceRows)
		if err != nil || !plan.hasWindow() {
			return rows, err
		}
		return plan.applyWindowsToGroupedRows(rows)
	}
	return collectWindowRows(plan, sourceRows)
}

func (p *relationalSelectPlan) hasWindow() bool {
	for _, projection := range p.projection {
		if projection.window != nil {
			return true
		}
	}
	return false
}

func windowDefinitionCount(projections []relationalProjection) int {
	return len(windowExplanationDefinitions(projections))
}

func windowExplanationDefinitions(projections []relationalProjection) []queryexplanation.Window {
	definitions := make([]queryexplanation.Window, 0)
	index := make(map[string]int)
	for _, projection := range projections {
		for _, window := range projectionWindowFunctions(projection) {
			definition := queryexplanation.Window{PartitionExpressions: append([]string(nil), window.spec.partition...), Orders: explanationWindowOrders(window.spec.order), Frame: explanationWindowFrame(window.spec.frame), Functions: []string{window.name}}
			key := windowExplanationKey(definition)
			if existing, found := index[key]; found {
				definitions[existing].Functions = append(definitions[existing].Functions, window.name)
				continue
			}
			index[key] = len(definitions)
			definitions = append(definitions, definition)
		}
	}
	return definitions
}

func projectionWindowFunctions(projection relationalProjection) []relationalWindowFunction {
	if len(projection.windowParts) == 0 {
		if projection.window == nil {
			return nil
		}
		return []relationalWindowFunction{*projection.window}
	}
	functions := make([]relationalWindowFunction, len(projection.windowParts))
	for index, part := range projection.windowParts {
		functions[index] = part.function
	}
	return functions
}

func explanationWindowOrders(orders []relationalWindowOrder) []queryexplanation.Order {
	result := make([]queryexplanation.Order, len(orders))
	for index, order := range orders {
		result[index] = queryexplanation.Order{Expression: order.expression, Direction: order.direction}
	}
	return result
}

func explanationWindowFrame(frame relationalWindowFrame) string {
	if !frame.present {
		return "default"
	}
	return strings.ToUpper(frame.mode) + " BETWEEN " + explanationWindowBound(frame.start) + " AND " + explanationWindowBound(frame.end)
}

func explanationWindowBound(bound relationalWindowBound) string {
	switch bound.kind {
	case "current_row":
		return "CURRENT ROW"
	case "unbounded_preceding":
		return "UNBOUNDED PRECEDING"
	case "unbounded_following":
		return "UNBOUNDED FOLLOWING"
	default:
		return strconv.Itoa(bound.offset) + " " + strings.ToUpper(bound.kind)
	}
}

func windowExplanationKey(window queryexplanation.Window) string {
	return strings.Join(window.PartitionExpressions, "\x00") + "\x01" + window.Frame + "\x01" + windowOrderKey(window.Orders)
}

func windowOrderKey(orders []queryexplanation.Order) string {
	parts := make([]string, len(orders))
	for index, order := range orders {
		parts[index] = order.Expression + "\x00" + order.Direction
	}
	return strings.Join(parts, "\x01")
}

func (p *relationalSelectPlan) applyWindowsToGroupedRows(rows []relationalResultRow) ([]relationalResultRow, error) {
	started := time.Now()
	if err := p.applyWindows(rows); err != nil {
		return nil, err
	}
	for index := range rows {
		if err := p.projectAggregateOrderValues(rows[index].group, &rows[index]); err != nil {
			return nil, err
		}
	}
	if p.runtime != nil {
		p.runtime.record(p.runtime.window, len(rows), len(rows), 0, 0, 0, resultMemory(rows), time.Since(started))
	}
	return rows, nil
}

func (p *relationalSelectPlan) hasAggregate() bool {
	for _, projection := range p.projection {
		if projection.aggregate != nil {
			return true
		}
	}
	return false
}

func collectFilteredSourceRows(plan *relationalSelectPlan) ([]relationRow, error) {
	started := time.Now()
	inputRows := 0
	memoryBytes := 0
	rows := make([]relationRow, 0)
	err := plan.forEachSourceRow(func(row relationRow) error {
		inputRows++
		if plan.where == nil {
			rows = append(rows, row)
			memoryBytes += relationRowMemory(row)
			return plan.session.observeBufferedMemory(memoryBytes)
		}
		matched, err := predicateMatches(plan.where, row)
		if err == nil && matched {
			rows = append(rows, row)
			memoryBytes += relationRowMemory(row)
			err = plan.session.observeBufferedMemory(memoryBytes)
		}
		return err
	})
	if err == nil && plan.runtime != nil && plan.where != nil {
		plan.runtime.record(plan.runtime.whereFilter, inputRows, len(rows), inputRows-len(rows), 0, 0, 0, time.Since(started))
	}
	return rows, err
}

func collectGroupedRows(plan *relationalSelectPlan, sourceRows []relationRow) ([]relationalResultRow, error) {
	started := time.Now()
	groups, err := groupSourceRows(plan, sourceRows)
	if err != nil {
		return nil, err
	}
	result := make([]relationalResultRow, 0, len(groups))
	for _, group := range groups {
		row, err := plan.projectAggregateGroup(group)
		if err != nil {
			return nil, err
		}
		matched, err := plan.groupMatchesHaving(group, row)
		if err != nil {
			return nil, err
		}
		if matched {
			result = append(result, row)
		}
	}
	if plan.runtime != nil {
		plan.runtime.record(plan.runtime.aggregate, len(sourceRows), len(groups), 0, 0, 0, resultMemory(result), time.Since(started))
		if plan.aggregation.having != "" {
			plan.runtime.record(plan.runtime.havingFilter, len(groups), len(result), len(groups)-len(result), 0, 0, 0, time.Since(started))
		}
		plan.runtime.record(plan.runtime.project, len(result), len(result), 0, 0, 0, resultMemory(result), time.Since(started))
	}
	return result, nil
}

func groupSourceRows(plan *relationalSelectPlan, rows []relationRow) ([][]relationRow, error) {
	if len(plan.aggregation.groups) == 0 {
		return [][]relationRow{rows}, nil
	}
	byKey := make(map[string]int)
	groups := make([][]relationRow, 0)
	for _, row := range rows {
		key, err := plan.groupKey(row)
		if err != nil {
			return nil, err
		}
		index, found := byKey[key]
		if !found {
			index, byKey[key] = len(groups), len(groups)
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], row)
	}
	return groups, nil
}

func (p *relationalSelectPlan) groupKey(row relationRow) (string, error) {
	var builder strings.Builder
	for _, group := range p.aggregation.groups {
		value, err := evaluateRelationExpressionContext(group.expression, p.source.columns, row, p.outer, p.session)
		if err != nil {
			return "", err
		}
		writeRelationalValueKey(&builder, group.expression, value, p.source.columns)
	}
	return builder.String(), nil
}

func (p *relationalSelectPlan) projectAggregateGroup(group []relationRow) (relationalResultRow, error) {
	source := sampleRelationRow(p.source.columns)
	if len(group) > 0 {
		source = group[0]
	}
	result := relationalResultRow{values: make([]string, len(p.projection)), nulls: make([]bool, len(p.projection)), source: source, group: group, projections: make([]exprValue, len(p.projection)), orders: make([]exprValue, len(p.order))}
	for index, projection := range p.projection {
		value, err := p.aggregateProjectionValue(projection, group, source)
		if err != nil {
			return relationalResultRow{}, err
		}
		setRelationalResultValue(&result, index, value)
	}
	return result, p.projectAggregateOrderValues(group, &result)
}

func (p *relationalSelectPlan) aggregateProjectionValue(projection relationalProjection, group []relationRow, source relationRow) (exprValue, error) {
	if projection.aggregate != nil {
		return p.evaluateAggregate(*projection.aggregate, group)
	}
	if projection.window != nil {
		return nullValue(), nil
	}
	return p.projectionValue(projection, source)
}

func (p *relationalSelectPlan) evaluateAggregate(aggregate relationalAggregate, rows []relationRow) (exprValue, error) {
	values, err := p.aggregateValues(aggregate, rows)
	if err != nil {
		return exprValue{}, err
	}
	if aggregate.name == "COUNT" {
		return intValue(int64(len(values))), nil
	}
	if len(values) == 0 {
		return nullValue(), nil
	}
	return aggregateResult(aggregate.name, values)
}

func (p *relationalSelectPlan) aggregateValues(aggregate relationalAggregate, rows []relationRow) ([]exprValue, error) {
	values := make([]exprValue, 0, len(rows))
	seen := make(map[string]struct{})
	arguments := aggregateArguments(aggregate)
	for _, row := range rows {
		value, included, err := p.aggregateRowValue(aggregate, arguments, row, seen)
		if err != nil {
			return nil, err
		}
		if included {
			values = append(values, value)
		}
	}
	return values, nil
}

func (p *relationalSelectPlan) aggregateRowValue(aggregate relationalAggregate, arguments []string, row relationRow, seen map[string]struct{}) (exprValue, bool, error) {
	if aggregate.argument == "*" {
		return intValue(1), true, nil
	}
	if len(arguments) > 1 {
		return p.aggregateTupleValue(arguments, row, seen)
	}
	return p.aggregateSingleValue(aggregate, row, seen)
}

func (p *relationalSelectPlan) aggregateTupleValue(arguments []string, row relationRow, seen map[string]struct{}) (exprValue, bool, error) {
	key, valid, err := p.aggregateTupleKey(arguments, row)
	if err != nil || !valid {
		return exprValue{}, false, err
	}
	if _, exists := seen[key]; exists {
		return exprValue{}, false, nil
	}
	seen[key] = struct{}{}
	return intValue(1), true, nil
}

func (p *relationalSelectPlan) aggregateSingleValue(aggregate relationalAggregate, row relationRow, seen map[string]struct{}) (exprValue, bool, error) {
	value, err := evaluateRelationExpressionContext(aggregate.argument, p.source.columns, row, p.outer, p.session)
	if err != nil || value.isNull() {
		return value, false, err
	}
	if aggregate.distinct && !aggregateValueNew(aggregate, value, seen, p.source.columns) {
		return value, false, nil
	}
	return value, true, nil
}

func aggregateValueNew(aggregate relationalAggregate, value exprValue, seen map[string]struct{}, columns []relationColumn) bool {
	key := relationalValueKey(aggregate.argument, value, columns)
	if _, exists := seen[key]; exists {
		return false
	}
	seen[key] = struct{}{}
	return true
}

func aggregateArguments(aggregate relationalAggregate) []string {
	if aggregate.name == "COUNT" && aggregate.distinct {
		return splitCSV(aggregate.argument)
	}
	return []string{aggregate.argument}
}

func (p *relationalSelectPlan) aggregateTupleKey(arguments []string, row relationRow) (string, bool, error) {
	var builder strings.Builder
	for _, argument := range arguments {
		value, err := evaluateRelationExpressionContext(argument, p.source.columns, row, p.outer, p.session)
		if err != nil {
			return "", false, err
		}
		if value.isNull() {
			return "", false, nil
		}
		writeRelationalValueKey(&builder, argument, value, p.source.columns)
	}
	return builder.String(), true, nil
}

func aggregateResult(name string, values []exprValue) (exprValue, error) {
	if name == "MIN" || name == "MAX" {
		return aggregateExtreme(name, values)
	}
	total := aggregateTotalStart(name, values[0])
	for _, value := range values[1:] {
		var err error
		total, err = arithmetic("+", total, value)
		if err != nil {
			return exprValue{}, err
		}
	}
	if name == "AVG" {
		return divideArithmetic(total, intValue(int64(len(values))))
	}
	return total, nil
}

func aggregateTotalStart(name string, value exprValue) exprValue {
	if (name == "SUM" || name == "AVG") && (value.kind == valueInt || value.kind == valueUint) {
		return decimalValueOf(toDecimal(value))
	}
	return value
}

func aggregateExtreme(name string, values []exprValue) (exprValue, error) {
	result := values[0]
	for _, value := range values[1:] {
		comparison, err := compareOperands(result, value)
		if err != nil {
			return exprValue{}, err
		}
		if (name == "MIN" && comparison > 0) || (name == "MAX" && comparison < 0) {
			result = value
		}
	}
	return result, nil
}

func (p *relationalSelectPlan) groupMatchesHaving(group []relationRow, result relationalResultRow) (bool, error) {
	if p.aggregation.having == "" {
		return true, nil
	}
	value, err := p.evaluateGroupExpression(p.aggregation.having, group, result)
	if err != nil {
		return false, err
	}
	known, truth, err := truthValue(value)
	return known && truth, err
}

func (p *relationalSelectPlan) evaluateGroupExpression(expression string, group []relationRow, result relationalResultRow) (exprValue, error) {
	replaced, err := p.replaceGroupAggregates(expression, group)
	if err != nil {
		return exprValue{}, err
	}
	return evaluateScalarWithResolver(replaced, func(name string) (exprValue, error) {
		if index, found := projectionIndex(p.projection, name); found {
			return result.projections[index], nil
		}
		if len(group) == 0 {
			return nullValue(), nil
		}
		return evaluateRelationExpressionContext(name, p.source.columns, group[0], p.outer, p.session)
	})
}

func (p *relationalSelectPlan) replaceGroupAggregates(expression string, group []relationRow) (string, error) {
	var result strings.Builder
	length := len(expression)
	for index := 0; index < length; {
		literal, end, found, err := p.groupAggregateLiteral(expression, index, group)
		if err != nil {
			return "", err
		}
		if found {
			result.WriteString(literal)
			index = end
			continue
		}
		result.WriteByte(expression[index])
		index++
	}
	return result.String(), nil
}

func (p *relationalSelectPlan) groupAggregateLiteral(expression string, index int, group []relationRow) (string, int, bool, error) {
	nameStart, nameEnd := aggregateNameAt(expression, index)
	if nameStart < 0 {
		return "", index, false, nil
	}
	open := afterAggregateName(expression, nameEnd)
	close, found := matchingParenthesis(expression, open)
	if open >= len(expression) || expression[open] != '(' || !found {
		return "", index, false, nil
	}
	aggregate, err := parseRelationalAggregate(strings.ToUpper(expression[nameStart:nameEnd]), expression[open+1:close])
	if err != nil {
		return "", index, false, err
	}
	value, err := p.evaluateAggregate(aggregate, group)
	if err != nil {
		return "", index, false, err
	}
	return groupExpressionLiteral(value), close + 1, true, nil
}

func afterAggregateName(expression string, index int) int {
	length := len(expression)
	for index < length && (expression[index] == ' ' || expression[index] == '\t') {
		index++
	}
	return index
}

func aggregateNameAt(expression string, index int) (int, int) {
	if index > 0 && isAggregateIdentifierByte(expression[index-1]) {
		return -1, -1
	}
	for _, name := range []string{"COUNT", "SUM", "AVG", "MIN", "MAX"} {
		end := index + len(name)
		if end <= len(expression) && strings.EqualFold(expression[index:end], name) && (end == len(expression) || !isAggregateIdentifierByte(expression[end])) {
			return index, end
		}
	}
	return -1, -1
}

func isAggregateIdentifierByte(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func groupExpressionLiteral(value exprValue) string {
	if value.isNull() {
		return "NULL"
	}
	if value.kind == valueString {
		return "'" + strings.ReplaceAll(value.render(), "'", "''") + "'"
	}
	return value.render()
}

func (p *relationalSelectPlan) projectAggregateOrderValues(group []relationRow, result *relationalResultRow) error {
	for index, order := range p.order {
		if order.fromProjection {
			result.orders[index] = result.projections[order.projection]
			continue
		}
		value, err := p.evaluateGroupExpression(order.expression, group, *result)
		if err != nil {
			return err
		}
		result.orders[index] = value
		p.order[index].computed = true
	}
	return nil
}

func collectWindowRows(plan *relationalSelectPlan, sourceRows []relationRow) ([]relationalResultRow, error) {
	started := time.Now()
	rows := make([]relationalResultRow, len(sourceRows))
	for index, source := range sourceRows {
		row, err := plan.projectWindowBaseRow(source)
		if err != nil {
			return nil, err
		}
		rows[index] = row
	}
	if err := plan.applyWindows(rows); err != nil {
		return nil, err
	}
	if plan.runtime != nil {
		plan.runtime.record(plan.runtime.window, len(sourceRows), len(rows), 0, 0, 0, resultMemory(rows), time.Since(started))
		plan.runtime.record(plan.runtime.project, len(rows), len(rows), 0, 0, 0, resultMemory(rows), time.Since(started))
	}
	return rows, nil
}

func (p *relationalSelectPlan) projectWindowBaseRow(source relationRow) (relationalResultRow, error) {
	result := relationalResultRow{values: make([]string, len(p.projection)), nulls: make([]bool, len(p.projection)), source: source, projections: make([]exprValue, len(p.projection)), orders: make([]exprValue, len(p.order))}
	for index, projection := range p.projection {
		if projection.window != nil {
			continue
		}
		value, err := p.projectionValue(projection, source)
		if err != nil {
			return relationalResultRow{}, err
		}
		setRelationalResultValue(&result, index, value)
	}
	return result, nil
}

func setRelationalResultValue(row *relationalResultRow, index int, value exprValue) {
	row.projections[index] = value
	if value.isNull() {
		row.values[index], row.nulls[index] = storedSQLNullValue, true
		return
	}
	row.values[index], row.nulls[index] = value.render(), false
}

func (p *relationalSelectPlan) applyWindows(rows []relationalResultRow) error {
	partitionsByWindow := make(map[string][][]int)
	for projectionIndex, projection := range p.projection {
		if projection.window == nil {
			continue
		}
		values, err := p.projectionWindowValues(rows, projection, partitionsByWindow)
		if err != nil {
			return err
		}
		for index, value := range values {
			setRelationalResultValue(&rows[index], projectionIndex, value)
		}
	}
	return nil
}

func (p *relationalSelectPlan) projectionWindowValues(rows []relationalResultRow, projection relationalProjection, cache map[string][][]int) ([]exprValue, error) {
	valuesByPlaceholder := make(map[string][]exprValue)
	for _, part := range projection.windowParts {
		values, err := p.windowFunctionValues(rows, part.function, cache)
		if err != nil {
			return nil, err
		}
		valuesByPlaceholder[part.placeholder] = values
	}
	if len(projection.windowParts) == 0 {
		return p.windowFunctionValues(rows, *projection.window, cache)
	}
	return p.composedWindowValues(rows, projection, valuesByPlaceholder)
}

func (p *relationalSelectPlan) windowFunctionValues(rows []relationalResultRow, function relationalWindowFunction, cache map[string][][]int) ([]exprValue, error) {
	values := make([]exprValue, len(rows))
	partitions, err := p.windowFunctionPartitions(rows, function, cache)
	if err != nil {
		return nil, err
	}
	for _, partition := range partitions {
		partitionValues, err := p.windowPartitionValues(rows, partition, function)
		if err != nil {
			return nil, err
		}
		for position, rowIndex := range partition {
			values[rowIndex] = partitionValues[position]
		}
	}
	return values, nil
}

func (p *relationalSelectPlan) composedWindowValues(rows []relationalResultRow, projection relationalProjection, values map[string][]exprValue) ([]exprValue, error) {
	result := make([]exprValue, len(rows))
	for index, row := range rows {
		windowValues := make(map[string]exprValue, len(values))
		for placeholder, functionValues := range values {
			windowValues[placeholder] = functionValues[index]
		}
		value, err := p.evaluateComposedWindowExpression(row.source, projection, windowValues)
		if err != nil {
			return nil, err
		}
		result[index] = value
	}
	return result, nil
}

func (p *relationalSelectPlan) windowFunctionPartitions(rows []relationalResultRow, function relationalWindowFunction, cached map[string][][]int) ([][]int, error) {
	key := relationalWindowSpecKey(function.spec)
	if partitions, found := cached[key]; found {
		return partitions, nil
	}
	partitions, err := p.windowPartitions(rows, function.spec)
	if err != nil {
		return nil, err
	}
	if err := p.sortWindowPartitions(rows, partitions, function.spec); err != nil {
		return nil, err
	}
	cached[key] = partitions
	return partitions, nil
}

func (p *relationalSelectPlan) sortWindowPartitions(rows []relationalResultRow, partitions [][]int, spec relationalWindowSpec) error {
	if len(spec.order) == 0 {
		return nil
	}
	started := time.Now()
	for _, partition := range partitions {
		if err := p.sortWindowPartition(rows, partition, spec); err != nil {
			return err
		}
	}
	if p.runtime != nil {
		p.runtime.record(p.runtime.windowSortID(spec), len(rows), len(rows), 0, 0, 0, resultMemory(rows), time.Since(started))
	}
	return nil
}

func relationalWindowSpecKey(spec relationalWindowSpec) string {
	orders := make([]string, len(spec.order))
	for index, order := range spec.order {
		orders[index] = order.expression + "\x00" + order.direction
	}
	return strings.Join(spec.partition, "\x00") + "\x01" + explanationWindowFrame(spec.frame) + "\x01" + strings.Join(orders, "\x01")
}

func (p *relationalSelectPlan) windowPartitions(rows []relationalResultRow, spec relationalWindowSpec) ([][]int, error) {
	byKey := make(map[string]int)
	partitions := make([][]int, 0)
	for index, row := range rows {
		key, err := p.windowPartitionKey(row, spec)
		if err != nil {
			return nil, err
		}
		partition, found := byKey[key]
		if !found {
			partition, byKey[key] = len(partitions), len(partitions)
			partitions = append(partitions, nil)
		}
		partitions[partition] = append(partitions[partition], index)
	}
	return partitions, nil
}

func (p *relationalSelectPlan) windowPartitionKey(row relationalResultRow, spec relationalWindowSpec) (string, error) {
	var builder strings.Builder
	for _, expression := range spec.partition {
		value, err := p.windowExpressionValue(row, expression)
		if err != nil {
			return "", err
		}
		writeRelationalValueKey(&builder, expression, value, p.source.columns)
	}
	return builder.String(), nil
}

func (p *relationalSelectPlan) windowExpressionValue(row relationalResultRow, expression string) (exprValue, error) {
	if index, found := projectionIndex(p.projection, expression); found && p.projection[index].window == nil {
		return row.projections[index], nil
	}
	function, tail, found := relationalFunction(expression)
	if found && strings.TrimSpace(tail) == "" && isAggregateName(strings.ToUpper(function.name)) && len(row.group) > 0 {
		aggregate, err := parseRelationalAggregate(strings.ToUpper(function.name), function.arguments)
		if err != nil {
			return exprValue{}, err
		}
		return p.evaluateAggregate(aggregate, row.group)
	}
	return evaluateRelationExpressionContext(expression, p.source.columns, row.source, p.outer, p.session)
}

func relationalValueKey(expression string, value exprValue, columns []relationColumn) string {
	var builder strings.Builder
	writeRelationalValueKey(&builder, expression, value, columns)
	return builder.String()
}

func writeRelationalValueKey(builder *strings.Builder, expression string, value exprValue, columns []relationColumn) {
	if value.isNull() {
		builder.WriteString("N;")
		return
	}
	key := value.render()
	if value.kind == valueString {
		key = relationalStringKey(expression, key, columns)
	}
	builder.WriteString(strconv.Itoa(int(value.kind)))
	builder.WriteByte(':')
	builder.WriteString(strconv.Itoa(len(key)))
	builder.WriteByte(':')
	builder.WriteString(key)
	builder.WriteByte(';')
}

func relationalStringKey(expression, value string, columns []relationColumn) string {
	column, err := resolveRelationColumn(expression, columns)
	if err != nil {
		return characterComparisonKey(defaultStringType, value)
	}
	typ, err := parseCharacterType(columns[column].typeName)
	if err != nil || typ.kind != characterText {
		return value
	}
	return characterComparisonKey(typ, value)
}

func (p *relationalSelectPlan) evaluateComposedWindowExpression(row relationRow, projection relationalProjection, windowValues map[string]exprValue) (exprValue, error) {
	return evaluateScalarWithResolver(projection.windowExpr, func(name string) (exprValue, error) {
		for placeholder, value := range windowValues {
			if identifiersEqual(name, placeholder) {
				return value, nil
			}
		}
		column, err := resolveRelationColumn(name, p.source.columns)
		if err != nil {
			return outerRelationValue(name, p.outer)
		}
		return relationColumnValue(p.source.columns, column, row)
	})
}

func (p *relationalSelectPlan) sortWindowPartition(rows []relationalResultRow, partition []int, spec relationalWindowSpec) error {
	orderValues := make(map[int][]exprValue, len(partition))
	for _, rowIndex := range partition {
		values := make([]exprValue, len(spec.order))
		for index, order := range spec.order {
			value, err := p.windowExpressionValue(rows[rowIndex], order.expression)
			if err != nil {
				return err
			}
			values[index] = value
		}
		orderValues[rowIndex] = values
	}
	sort.SliceStable(partition, func(left, right int) bool {
		for index, order := range spec.order {
			comparison := p.compareWindowOrderValues(order, orderValues[partition[left]][index], orderValues[partition[right]][index])
			if comparison != 0 {
				return orderedBefore(comparison, order.direction)
			}
		}
		return false
	})
	return nil
}

func (p *relationalSelectPlan) windowValue(rows []relationalResultRow, partition []int, position int, function relationalWindowFunction) (exprValue, error) {
	switch function.name {
	case "ROW_NUMBER":
		return intValue(int64(position + 1)), nil
	case "RANK", "DENSE_RANK":
		return p.windowRankValue(rows, partition, position, function)
	case "LAG", "LEAD":
		return p.windowOffsetValue(rows, partition, position, function)
	case "FIRST_VALUE", "LAST_VALUE", "NTH_VALUE":
		return p.windowPositionalValue(rows, partition, position, function)
	default:
		return p.windowAggregateValue(rows, partition, position, function)
	}

}

func (p *relationalSelectPlan) windowRankValue(rows []relationalResultRow, partition []int, position int, function relationalWindowFunction) (exprValue, error) {
	dense := function.name == "DENSE_RANK"
	rank, err := p.windowRank(rows, partition, position, function.spec, dense)
	if err != nil {
		return exprValue{}, err
	}
	return intValue(rank), nil
}

func (p *relationalSelectPlan) windowAggregateValue(rows []relationalResultRow, partition []int, position int, function relationalWindowFunction) (exprValue, error) {
	frame, err := p.windowFrameIndexes(rows, partition, position, function.spec)
	if err != nil {
		return exprValue{}, err
	}
	group := make([]relationRow, len(frame))
	for index, rowIndex := range frame {
		group[index] = rows[rowIndex].source
	}
	if len(rows[partition[position]].group) > 0 {
		return p.evaluateGroupedWindowAggregate(function.relationalAggregate, rows, frame)
	}
	return p.evaluateAggregate(function.relationalAggregate, group)
}

func (p *relationalSelectPlan) evaluateGroupedWindowAggregate(aggregate relationalAggregate, rows []relationalResultRow, frame []int) (exprValue, error) {
	values := make([]exprValue, 0, len(frame))
	seen := make(map[string]struct{})
	for _, rowIndex := range frame {
		if aggregate.argument == "*" {
			values = append(values, intValue(1))
			continue
		}
		value, err := p.windowExpressionValue(rows[rowIndex], aggregate.argument)
		if err != nil {
			return exprValue{}, err
		}
		if value.isNull() {
			continue
		}
		key := relationalValueKey(aggregate.argument, value, p.source.columns)
		if aggregate.distinct {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		values = append(values, value)
	}
	if aggregate.name == "COUNT" {
		return intValue(int64(len(values))), nil
	}
	if len(values) == 0 {
		return nullValue(), nil
	}
	return aggregateResult(aggregate.name, values)
}

func (p *relationalSelectPlan) windowFrameIndexes(rows []relationalResultRow, partition []int, position int, spec relationalWindowSpec) ([]int, error) {
	frame := spec.frame
	if !frame.present {
		frame = relationalWindowFrame{mode: "rows", start: relationalWindowBound{kind: "unbounded_preceding"}, end: relationalWindowBound{kind: "unbounded_following"}}
		if len(spec.order) > 0 {
			frame.mode, frame.end = "range", relationalWindowBound{kind: "current_row"}
		}
	}
	start, end := windowFrameBounds(position, len(partition), frame)
	if frame.mode == "range" {
		return p.rangeFrameIndexes(rows, partition, position, spec, frame, start, end)
	}
	if start > end {
		return nil, nil
	}
	return partition[start : end+1], nil
}

func windowFrameBounds(position, length int, frame relationalWindowFrame) (int, int) {
	start := windowFrameBoundPosition(position, length, frame.start, true)
	end := windowFrameBoundPosition(position, length, frame.end, false)
	if start < 0 {
		start = 0
	}
	if end >= length {
		end = length - 1
	}
	return start, end
}

func windowFrameBoundPosition(position, length int, bound relationalWindowBound, start bool) int {
	switch bound.kind {
	case "unbounded_preceding":
		return 0
	case "unbounded_following":
		return length - 1
	case "preceding":
		return position - bound.offset
	case "following":
		return position + bound.offset
	default:
		return position
	}
}

func (p *relationalSelectPlan) rangeFrameIndexes(rows []relationalResultRow, partition []int, position int, spec relationalWindowSpec, frame relationalWindowFrame, start, end int) ([]int, error) {
	if len(spec.order) == 0 {
		return partition, nil
	}
	if !rangeFrameHasOffset(frame) {
		start, end, peerErr := p.expandCurrentRangePeers(rows, partition, position, spec, frame, start, end)
		if peerErr != nil {
			return nil, peerErr
		}
		return partition[start : end+1], nil
	}
	if len(spec.order) != 1 {
		return nil, sqlFailure{3587, "HY000", "RANGE frame with value offset requires one ORDER BY expression"}
	}
	return p.numericRangeFrame(rows, partition, position, spec.order[0], frame)
}

func rangeFrameHasOffset(frame relationalWindowFrame) bool {
	return frame.start.kind == "preceding" || frame.start.kind == "following" || frame.end.kind == "preceding" || frame.end.kind == "following"
}

func (p *relationalSelectPlan) expandCurrentRangePeers(rows []relationalResultRow, partition []int, position int, spec relationalWindowSpec, frame relationalWindowFrame, start, end int) (int, int, error) {
	partitionLength := len(partition)
	if frame.start.kind == "current_row" {
		for start > 0 {
			tied, err := p.windowResultRowsTie(rows[partition[start-1]], rows[partition[position]], spec)
			if err != nil {
				return 0, 0, err
			}
			if !tied {
				break
			}
			start--
		}
	}
	if frame.end.kind == "current_row" {
		for end+1 < partitionLength {
			tied, err := p.windowResultRowsTie(rows[partition[end+1]], rows[partition[position]], spec)
			if err != nil {
				return 0, 0, err
			}
			if !tied {
				break
			}
			end++
		}
	}
	return start, end, nil
}

func (p *relationalSelectPlan) numericRangeFrame(rows []relationalResultRow, partition []int, position int, order relationalWindowOrder, frame relationalWindowFrame) ([]int, error) {
	current, err := p.windowExpressionValue(rows[partition[position]], order.expression)
	if err != nil {
		return nil, sqlFailure{3587, "HY000", "RANGE frame with value offset requires numeric ORDER BY values"}
	}
	if current.isNull() {
		return p.nullNumericRangeFrame(rows, partition, position, order)
	}
	lower, lowerOpen, err := rangeFrameBoundary(current, frame.start, order.direction, true)
	if err != nil {
		return nil, err
	}
	upper, upperOpen, err := rangeFrameBoundary(current, frame.end, order.direction, false)
	if err != nil {
		return nil, err
	}
	result := make([]int, 0, len(partition))
	for _, rowIndex := range partition {
		value, valueErr := p.windowExpressionValue(rows[rowIndex], order.expression)
		if valueErr != nil {
			return nil, valueErr
		}
		if value.isNull() || !rangeValueIncluded(value, lower, lowerOpen, upper, upperOpen, order.direction) {
			continue
		}
		result = append(result, rowIndex)
	}
	return result, nil
}

func (p *relationalSelectPlan) nullNumericRangeFrame(rows []relationalResultRow, partition []int, position int, order relationalWindowOrder) ([]int, error) {
	frame := relationalWindowFrame{start: relationalWindowBound{kind: "current_row"}, end: relationalWindowBound{kind: "current_row"}}
	start, end, err := p.expandCurrentRangePeers(rows, partition, position, relationalWindowSpec{order: []relationalWindowOrder{order}}, frame, position, position)
	if err != nil {
		return nil, err
	}
	return partition[start : end+1], nil
}

func rangeFrameBoundary(current exprValue, bound relationalWindowBound, direction string, start bool) (exprValue, bool, error) {
	if bound.kind == "unbounded_preceding" || bound.kind == "unbounded_following" {
		return exprValue{}, true, nil
	}
	delta := rangeFrameDelta(bound, direction)
	if delta == 0 {
		return current, false, nil
	}
	value, err := arithmetic("+", current, intValue(int64(delta)))
	if err != nil {
		return exprValue{}, false, sqlFailure{3587, "HY000", "RANGE frame with value offset requires numeric ORDER BY values"}
	}
	return value, false, nil
}

func rangeFrameDelta(bound relationalWindowBound, direction string) int {
	directionSign := 1
	if direction == "DESC" {
		directionSign = -1
	}
	if bound.kind == "preceding" {
		return -directionSign * bound.offset
	}
	if bound.kind == "following" {
		return directionSign * bound.offset
	}
	return 0
}

func rangeValueIncluded(value, start exprValue, startOpen bool, end exprValue, endOpen bool, direction string) bool {
	if !startOpen {
		comparison, err := compareWindowRangeValue(value, start, direction)
		if err != nil || comparison < 0 {
			return false
		}
	}
	if !endOpen {
		comparison, err := compareWindowRangeValue(value, end, direction)
		if err != nil || comparison > 0 {
			return false
		}
	}
	return true
}

func compareWindowRangeValue(value, boundary exprValue, direction string) (int, error) {
	comparison, err := compareOperands(value, boundary)
	if err != nil || direction != "DESC" {
		return comparison, err
	}
	return -comparison, nil
}

func (p *relationalSelectPlan) windowRank(rows []relationalResultRow, partition []int, position int, spec relationalWindowSpec, dense bool) (int64, error) {
	rank := int64(1)
	for index := 1; index <= position; index++ {
		tied, err := p.windowResultRowsTie(rows[partition[index-1]], rows[partition[index]], spec)
		if err != nil {
			return 0, err
		}
		if tied {
			continue
		}
		if dense {
			rank++
		} else {
			rank = int64(index + 1)
		}
	}
	return rank, nil
}

func (p *relationalSelectPlan) windowResultRowsTie(left, right relationalResultRow, spec relationalWindowSpec) (bool, error) {
	for _, order := range spec.order {
		leftValue, leftErr := p.windowExpressionValue(left, order.expression)
		rightValue, rightErr := p.windowExpressionValue(right, order.expression)
		if leftErr != nil {
			return false, leftErr
		}
		if rightErr != nil {
			return false, rightErr
		}
		comparison := p.compareWindowOrderValues(order, leftValue, rightValue)
		if comparison != 0 {
			return false, nil
		}
	}
	return true, nil
}

func (p *relationalSelectPlan) compareWindowOrderValues(order relationalWindowOrder, left, right exprValue) int {
	if left.kind == valueString && right.kind == valueString {
		return strings.Compare(relationalStringKey(order.expression, left.s, p.source.columns), relationalStringKey(order.expression, right.s, p.source.columns))
	}
	return compareWindowValues(left, right)
}

func compareWindowValues(left, right exprValue) int {
	if left.isNull() || right.isNull() {
		switch {
		case left.isNull() && right.isNull():
			return 0
		case left.isNull():
			return -1
		default:
			return 1
		}
	}
	comparison, err := compareOperands(left, right)
	if err != nil {
		return strings.Compare(left.render(), right.render())
	}
	return comparison
}

func (p *relationalSelectPlan) windowOffsetValue(rows []relationalResultRow, partition []int, position int, function relationalWindowFunction) (exprValue, error) {
	arguments := splitCSV(function.argument)
	if len(arguments) < 1 || len(arguments) > 3 {
		return exprValue{}, sqlFailure{1064, "42000", "invalid window arguments"}
	}
	offset, err := windowOffset(arguments)
	if err != nil {
		return exprValue{}, err
	}
	target, found := windowOffsetTarget(position, offset, function.name)
	if found && target < len(partition) {
		return p.windowExpressionValue(rows[partition[target]], arguments[0])
	}
	return p.windowOffsetDefault(arguments, rows[partition[position]].source)
}

func windowOffset(arguments []string) (uint64, error) {
	if len(arguments) == 1 {
		return 1, nil
	}
	offset, valid := windowUnsignedIntegerLiteral(arguments[1])
	if !valid {
		return 0, sqlFailure{1210, "HY000", "Incorrect arguments to " + "LAG or LEAD"}
	}
	return offset, nil
}

func windowUnsignedIntegerLiteral(expression string) (uint64, bool) {
	value := strings.TrimSpace(expression)
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil && parsed <= uint64(math.MaxInt64)+1
}

func windowOffsetTarget(position int, offset uint64, name string) (int, bool) {
	if name == "LEAD" {
		target := uint64(position) + offset
		if target > uint64(maxIntValue()) {
			return 0, false
		}
		return int(target), true
	}
	if offset > uint64(position) {
		return 0, false
	}
	return position - int(offset), true
}

func (p *relationalSelectPlan) windowOffsetDefault(arguments []string, row relationRow) (exprValue, error) {
	if len(arguments) == 3 {
		return evaluateRelationExpressionContext(arguments[2], p.source.columns, row, p.outer, p.session)
	}
	return nullValue(), nil
}

func (p *relationalSelectPlan) windowPositionalValue(rows []relationalResultRow, partition []int, position int, function relationalWindowFunction) (exprValue, error) {
	arguments := splitCSV(function.argument)
	if len(arguments) == 0 || len(arguments) > 2 || (function.name != "NTH_VALUE" && len(arguments) != 1) {
		return exprValue{}, sqlFailure{1064, "42000", "invalid window arguments"}
	}
	frame, err := p.windowFrameIndexes(rows, partition, position, function.spec)
	if err != nil {
		return exprValue{}, err
	}
	if len(frame) == 0 {
		return nullValue(), nil
	}
	target, valid, err := positionalWindowTarget(function.name, arguments, len(frame))
	if err != nil {
		return exprValue{}, err
	}
	if !valid {
		return nullValue(), nil
	}
	return p.windowExpressionValue(rows[frame[target]], arguments[0])
}

func positionalWindowTarget(name string, arguments []string, frameLength int) (int, bool, error) {
	switch name {
	case "LAST_VALUE":
		return frameLength - 1, true, nil
	case "NTH_VALUE":
		return nthWindowTarget(arguments, frameLength)
	default:
		return 0, true, nil
	}
}

func nthWindowTarget(arguments []string, frameLength int) (int, bool, error) {
	if len(arguments) != 2 {
		return 0, false, sqlFailure{1064, "42000", "invalid NTH_VALUE arguments"}
	}
	value, valid := windowUnsignedIntegerLiteral(arguments[1])
	if !valid || value == 0 {
		return 0, false, sqlFailure{1210, "HY000", "incorrect arguments to NTH_VALUE"}
	}
	if value > uint64(frameLength) {
		return 0, false, nil
	}
	return int(value - 1), true, nil
}
