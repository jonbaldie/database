package mysql

import (
	"errors"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

const maximumComposedQueryDepth = 64

type composedQueryContext struct {
	executor    *relationExecutor
	ctes        map[string]composedRelation
	depth       int
	planning    bool
	strictScope bool
}

type composedRelation struct {
	name       string
	query      string
	reason     string
	result     *queryResult
	ctes       map[string]composedRelation
	references int
	state      *composedRelationState
}

type composedRelationState struct{ result *queryResult }

type outerRelationScope struct {
	columns []relationColumn
	row     relationRow
	parent  *outerRelationScope
}

type setQuery struct {
	terms      []string
	operations []setQueryOperation
	order      string
	limit      string
}

type setQueryOperation struct {
	kind setOperationKind
	all  bool
}

type setOperationKind string

const (
	setUnion     setOperationKind = "union"
	setIntersect setOperationKind = "intersect"
	setExcept    setOperationKind = "except"
)

func newComposedQueryContext(executor *relationExecutor) *composedQueryContext {
	return &composedQueryContext{executor: executor, ctes: make(map[string]composedRelation)}
}

func (context *composedQueryContext) child() (*composedQueryContext, error) {
	if context.depth >= maximumComposedQueryDepth {
		return nil, sqlFailure{1473, "HY000", "query nesting exceeds the v0.1 limit"}
	}
	executor := *context.executor
	executor.streamRows = false
	return &composedQueryContext{executor: &executor, ctes: context.ctes, depth: context.depth + 1, planning: context.planning, strictScope: context.strictScope}, nil
}

func executeComposedSelect(context *composedQueryContext, query string, outer *outerRelationScope) (*queryResult, error) {
	query = stripWholeQueryParentheses(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	local, body, err := parseAndMaterializeCTEs(context, query, outer)
	if err != nil {
		return nil, err
	}
	if parsed, ok, err := parseSetQuery(body); err != nil {
		return nil, err
	} else if ok {
		return executeSetQuery(local, parsed, outer)
	}
	return executeSelectTerm(local, body, outer)
}

// describeComposedSelect plans a composed query and returns only its result
// shape. It never visits source rows or evaluates subqueries.
func describeComposedSelect(context *composedQueryContext, query string, outer *outerRelationScope) (*queryResult, error) {
	if context == nil {
		return nil, unsupportedExpression()
	}
	planning := *context
	planning.planning = true
	planning.ctes = cloneComposedRelations(context.ctes)
	query = stripWholeQueryParentheses(strings.TrimSpace(strings.TrimSuffix(query, ";")))
	local, body, err := parseAndMaterializeCTEs(&planning, query, outer)
	if err != nil {
		return nil, err
	}
	if parsed, ok, err := parseSetQuery(body); err != nil {
		return nil, err
	} else if ok {
		return describeSetQuery(local, parsed, outer)
	}
	return describeSelectTerm(local, body, outer)
}

func composedQueryIsCorrelated(context *composedQueryContext, query string, outer *outerRelationScope) bool {
	if outer == nil {
		return false
	}
	probe := *context
	probe.strictScope = true
	_, err := describeComposedSelect(&probe, query, nil)
	var failure sqlFailure
	if !errors.As(err, &failure) || failure.code != 1054 {
		return false
	}
	_, err = describeComposedSelect(&probe, query, outer)
	return err == nil
}

func describeSelectTerm(context *composedQueryContext, query string, outer *outerRelationScope) (*queryResult, error) {
	query = stripWholeQueryParentheses(strings.TrimSpace(query))
	if strings.HasPrefix(strings.ToLower(query), "with ") {
		return describeComposedSelect(context, query, outer)
	}
	if !strings.HasPrefix(strings.ToLower(query), "select ") {
		return nil, sqlFailure{1064, "42000", "composed query term must be SELECT"}
	}
	expression := strings.TrimSpace(query[len("SELECT "):])
	if keywordAt(expression, "from") < 0 {
		return describeScalarSelect(context, expression, outer)
	}
	executor := *context.executor
	executor.composed = context
	plan, err := parseRelationalSelectContext(&executor, query, outer)
	if err != nil {
		return nil, err
	}
	columns, metadata := plan.resultColumns()
	return &queryResult{columns: columns, metadata: metadata}, nil
}

func describeScalarSelect(context *composedQueryContext, expression string, outer *outerRelationScope) (*queryResult, error) {
	items := splitCSV(expression)
	columns := make([]string, len(items))
	metadata := make([]columnMetadata, len(items))
	for index, item := range items {
		expression, alias, err := splitProjectionAlias(item)
		if err != nil {
			return nil, err
		}
		name := expression
		if alias != "" {
			name = alias
		}
		if query, ok := scalarSubquerySQL(expression); ok {
			result, err := describeComposedSelect(context, query, outer)
			if err != nil {
				return nil, err
			}
			if len(result.columns) != 1 {
				return nil, sqlFailure{1241, "21000", "operand should contain 1 column"}
			}
			metadata[index] = resultColumnDefinition(result.columns[0], 0, result.metadata)
			metadata[index].flags &^= mysqlNotNullFlag
		} else {
			metadata[index], err = plannedScalarMetadata(expression, context.strictScope, outer)
			if err != nil {
				return nil, err
			}
		}
		columns[index], metadata[index].name = name, name
	}
	return &queryResult{columns: columns, metadata: metadata}, nil
}

func plannedScalarMetadata(expression string, strictScope bool, outer *outerRelationScope) (columnMetadata, error) {
	trimmed := strings.TrimSpace(expression)
	if value, err := evaluateScalar(trimmed); err == nil {
		return scalarMetadata(trimmed, value.render(), value), nil
	}
	if outer != nil {
		if value, err := evaluateScalarWithResolver(trimmed, func(name string) (exprValue, error) { return outerRelationValue(name, outer) }); err == nil {
			metadata := scalarMetadata(trimmed, value.render(), value)
			metadata.flags &^= mysqlNotNullFlag
			return metadata, nil
		}
	}
	if strictScope {
		_, err := evaluateScalarWithResolver(trimmed, func(name string) (exprValue, error) { return exprValue{}, unknownColumnError(name) })
		var failure sqlFailure
		if errors.As(err, &failure) && failure.code == 1054 {
			return columnMetadata{}, err
		}
	}
	return columnMetadata{catalog: "def", name: trimmed, characterSet: mysqlCharsetUTF8MB40900AICI, length: 255, typ: mysqlTypeVarString}, nil
}

func describeSetQuery(context *composedQueryContext, query setQuery, outer *outerRelationScope) (*queryResult, error) {
	results := make([]*queryResult, len(query.terms))
	for index, term := range query.terms {
		result, err := describeComposedSelect(context, term, outer)
		if err != nil {
			return nil, err
		}
		results[index] = result
	}
	return reduceSetResults(results, append([]setQueryOperation(nil), query.operations...))
}

func executeSelectTerm(context *composedQueryContext, query string, outer *outerRelationScope) (*queryResult, error) {
	query = stripWholeQueryParentheses(strings.TrimSpace(query))
	lower := strings.ToLower(query)
	if strings.HasPrefix(lower, "with ") {
		return executeComposedSelect(context, query, outer)
	}
	if !strings.HasPrefix(lower, "select ") {
		return nil, sqlFailure{1064, "42000", "composed query term must be SELECT"}
	}
	executor := *context.executor
	executor.composed = context
	expression := strings.TrimSpace(query[len("SELECT "):])
	if from := keywordAt(expression, "from"); from >= 0 {
		source := strings.TrimSpace(expression[from+len("from"):])
		if isInformationSchemaSource(source) {
			return selectFrom(&executor, query, expression[:from], source)
		}
		return executeRelationalSelectContext(&executor, query, outer)
	}
	return executeScalarSelectContext(context, expression, outer)
}

func executeScalarSelectContext(context *composedQueryContext, expression string, outer *outerRelationScope) (*queryResult, error) {
	items := splitCSV(expression)
	columns := make([]string, len(items))
	row := make([]string, len(items))
	nulls := make([]bool, len(items))
	metadata := make([]columnMetadata, len(items))
	for index, item := range items {
		expression, alias, err := splitProjectionAlias(item)
		if err != nil {
			return nil, err
		}
		value, definition, err := evaluateComposedScalar(context, expression, outer)
		if err != nil {
			return nil, err
		}
		name := expression
		if alias != "" {
			name = alias
		}
		definition.name = name
		columns[index], metadata[index] = name, definition
		if value.isNull() {
			row[index], nulls[index] = storedSQLNullValue, true
		} else {
			row[index] = value.render()
		}
	}
	return &queryResult{columns: columns, rows: [][]string{row}, nulls: [][]bool{nulls}, metadata: metadata}, nil
}

func evaluateComposedScalar(context *composedQueryContext, expression string, outer *outerRelationScope) (exprValue, columnMetadata, error) {
	if query, ok := scalarSubquerySQL(expression); ok {
		return executeScalarSubquery(context, query, outer)
	}
	value, err := evaluateScalarWithResolver(expression, func(name string) (exprValue, error) {
		return outerRelationValue(name, outer)
	})
	if err != nil {
		return exprValue{}, columnMetadata{}, err
	}
	return value, scalarMetadata(expression, value.render(), value), nil
}

func executeScalarSubquery(context *composedQueryContext, query string, outer *outerRelationScope) (exprValue, columnMetadata, error) {
	if context == nil {
		return exprValue{}, columnMetadata{}, unsupportedExpression()
	}
	child, err := context.child()
	if err != nil {
		return exprValue{}, columnMetadata{}, err
	}
	result, err := executeComposedSelect(child, query, outer)
	if err != nil {
		return exprValue{}, columnMetadata{}, err
	}
	if len(result.columns) != 1 {
		return exprValue{}, columnMetadata{}, sqlFailure{1241, "21000", "operand should contain 1 column"}
	}
	definition := resultColumnDefinition(result.columns[0], 0, result.metadata)
	if len(result.rows) > 1 {
		return exprValue{}, columnMetadata{}, sqlFailure{1242, "21000", "subquery returns more than 1 row"}
	}
	if len(result.rows) == 0 || resultValueIsNull(0, 0, result.nulls) {
		definition.flags &^= mysqlNotNullFlag
		return nullValue(), definition, nil
	}
	value, err := expressionValueFromMetadata(result.rows[0][0], definition)
	return value, definition, err
}

func compileExistsSubquery(text string, columns []relationColumn, context *composedQueryContext, outer *outerRelationScope) (relationPredicate, bool, error) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(trimmed), "exists ") {
		return nil, false, nil
	}
	query, ok := scalarSubquerySQL(strings.TrimSpace(trimmed[len("EXISTS "):]))
	if !ok || context == nil {
		return nil, true, unsupportedExpression()
	}
	scope, err := validatedExistsScope(context, query, columns, outer)
	if err != nil {
		return nil, true, err
	}
	if !context.planning && !composedQueryIsCorrelated(context, existsProjectionQuery(query), scope) {
		predicate, err := compileCachedExists(context, query)
		if err != nil {
			return nil, true, err
		}
		return predicate, true, nil
	}
	return func(row relationRow) (exprValue, error) {
		child, err := context.child()
		if err != nil {
			return exprValue{}, err
		}
		scope := &outerRelationScope{columns: columns, row: row, parent: outer}
		found, err := executeExistsSubquery(child, query, scope)
		return boolValue(found), err
	}, true, nil
}

func validatedExistsScope(context *composedQueryContext, query string, columns []relationColumn, outer *outerRelationScope) (*outerRelationScope, error) {
	scope := &outerRelationScope{columns: columns, row: sampleRelationRow(columns), parent: outer}
	if err := validateSubqueryScope(context, query, scope); err != nil {
		return nil, err
	}
	if _, err := describeComposedSelect(context, existsProjectionQuery(query), scope); err != nil {
		return nil, err
	}
	return scope, nil
}

func compileCachedExists(context *composedQueryContext, query string) (relationPredicate, error) {
	child, err := context.child()
	if err != nil {
		return nil, err
	}
	found, err := executeExistsSubquery(child, query, nil)
	if err != nil {
		return nil, err
	}
	return func(relationRow) (exprValue, error) { return boolValue(found), nil }, nil
}

func validateSubqueryScope(context *composedQueryContext, query string, outer *outerRelationScope) error {
	probe := *context
	probe.strictScope = true
	_, err := describeComposedSelect(&probe, query, outer)
	var failure sqlFailure
	if errors.As(err, &failure) && failure.code == 1054 {
		return err
	}
	return nil
}

var errExistsRow = errors.New("EXISTS found a row")

func executeExistsSubquery(context *composedQueryContext, query string, outer *outerRelationScope) (bool, error) {
	local, body, err := parseAndMaterializeCTEs(context, strings.TrimSpace(query), outer)
	if err != nil {
		return false, err
	}
	body = existsProjectionQuery(body)
	local.executor.streamRows = true
	result, err := executeComposedSelect(local, body, outer)
	if err != nil {
		return false, err
	}
	if result.stream == nil {
		return len(result.rows) > 0, nil
	}
	err = result.stream(func([]string, []bool) error { return errExistsRow })
	if errors.Is(err, errExistsRow) {
		return true, nil
	}
	return false, err
}

func existsProjectionQuery(query string) string {
	query = stripWholeQueryParentheses(strings.TrimSpace(query))
	if parsed, set, _ := parseSetQuery(query); set {
		if rewritten, ok := existsUnionQuery(parsed); ok {
			return rewritten
		}
		return query
	}
	if !strings.HasPrefix(strings.ToLower(query), "select ") {
		return query
	}
	expression := strings.TrimSpace(query[len("SELECT "):])
	from := keywordAt(expression, "from")
	if from < 0 {
		return "SELECT 1"
	}
	return "SELECT 1 " + strings.TrimSpace(expression[from:])
}

func existsUnionQuery(query setQuery) (string, bool) {
	allOperations := true
	for _, operation := range query.operations {
		if operation.kind != setUnion {
			return "", false
		}
		allOperations = allOperations && operation.all
	}
	if !allOperations && setLimitHasOffset(query.limit) {
		return "", false
	}
	terms := make([]string, len(query.terms))
	for index, term := range query.terms {
		trimmed := strings.TrimSpace(term)
		inner := stripWholeQueryParentheses(trimmed)
		terms[index] = existsProjectionQuery(inner)
		if inner != trimmed {
			terms[index] = "(" + terms[index] + ")"
		}
	}
	rewritten := strings.Join(terms, " UNION ALL ")
	if strings.TrimSpace(query.limit) != "" {
		rewritten += " LIMIT " + query.limit
	}
	return rewritten, true
}

func setLimitHasOffset(limit string) bool {
	lower := strings.ToLower(limit)
	return strings.Contains(limit, ",") || strings.Contains(lower, " offset ")
}

func compileInSubquery(text string, columns []relationColumn, session *session, context *composedQueryContext, outer *outerRelationScope) (relationPredicate, bool, error) {
	found, left, right := splitRelationKeywordOnce(strings.TrimSpace(text), "IN")
	if !found {
		return nil, false, nil
	}
	query, ok := scalarSubquerySQL(right)
	if !ok {
		return nil, false, nil
	}
	left, negate := stripNotIn(left)
	operand, err := compileRelationOperandContext(left, columns, session, outer)
	if err != nil {
		return nil, true, err
	}
	if context == nil {
		return nil, true, unsupportedExpression()
	}
	plan := inSubqueryPredicate{operand: operand, columns: columns, context: context, outer: outer, query: query, negate: negate}
	scope := &outerRelationScope{columns: columns, row: sampleRelationRow(columns), parent: outer}
	if _, err := describeComposedSelect(context, query, scope); err != nil {
		return nil, true, err
	}
	if !context.planning && !composedQueryIsCorrelated(context, query, scope) {
		plan.cached, err = plan.executeScope(nil)
		if err != nil {
			return nil, true, err
		}
	}
	return plan.evaluate, true, nil
}

type inSubqueryPredicate struct {
	operand relationOperand
	columns []relationColumn
	context *composedQueryContext
	outer   *outerRelationScope
	query   string
	negate  bool
	cached  *queryResult
}

func stripNotIn(left string) (string, bool) {
	left = strings.TrimSpace(left)
	if !strings.HasSuffix(strings.ToLower(left), " not") {
		return left, false
	}
	return strings.TrimSpace(left[:len(left)-len(" not")]), true
}

func (plan inSubqueryPredicate) evaluate(row relationRow) (exprValue, error) {
	left, err := relationOperandValue(plan.operand, row)
	if err != nil {
		return exprValue{}, err
	}
	result, err := plan.execute(row)
	if err != nil {
		return exprValue{}, err
	}
	return evaluateInMembership(left, result, plan.negate, plan.operand)
}

func (plan inSubqueryPredicate) execute(row relationRow) (*queryResult, error) {
	if plan.cached != nil {
		return plan.cached, nil
	}
	scope := &outerRelationScope{columns: plan.columns, row: row, parent: plan.outer}
	return plan.executeScope(scope)
}

func (plan inSubqueryPredicate) executeScope(scope *outerRelationScope) (*queryResult, error) {
	child, err := plan.context.child()
	if err != nil {
		return nil, err
	}
	result, err := executeComposedSelect(child, plan.query, scope)
	if err != nil {
		return nil, err
	}
	result, err = materializeQueryResult(result)
	if err != nil {
		return nil, err
	}
	if len(result.columns) != 1 {
		return nil, sqlFailure{1241, "21000", "operand should contain 1 column"}
	}
	return result, nil
}

func evaluateInMembership(left exprValue, result *queryResult, negate bool, operand relationOperand) (exprValue, error) {
	matched, unknown := false, left.isNull()
	metadata := resultColumnDefinition(result.columns[0], 0, result.metadata)
	for index := range result.rows {
		match, rowUnknown, err := compareInRow(left, result, metadata, index, operand)
		if err != nil {
			return exprValue{}, err
		}
		matched, unknown = matched || match, unknown || rowUnknown
		if matched {
			return boolValue(!negate), nil
		}
	}
	if unknown {
		return nullValue(), nil
	}
	return boolValue(negate), nil
}

func compareInRow(left exprValue, result *queryResult, metadata columnMetadata, index int, operand relationOperand) (bool, bool, error) {
	if resultValueIsNull(index, 0, result.nulls) {
		return false, true, nil
	}
	right, err := expressionValueFromMetadata(result.rows[index][0], metadata)
	if err != nil {
		return false, false, err
	}
	comparison, err := compareInValues(left, right, metadata, operand)
	if err != nil {
		return false, false, err
	}
	known, truth, err := truthValue(comparison)
	return known && truth, !known, err
}

func compareInValues(left, right exprValue, metadata columnMetadata, operand relationOperand) (exprValue, error) {
	if left.isNull() || right.isNull() || left.kind != valueString || right.kind != valueString {
		return compareValues("=", left, right)
	}
	characterType := defaultStringType
	if operand.isColumn {
		if parsed, err := parseCharacterType(operand.definition.typeName); err == nil && parsed.kind == characterText {
			characterType = parsed
		}
	} else if metadata.characterSet == mysqlCharsetUTF8MB4Bin {
		characterType.collation = collationBin
	}
	return boolValue(characterComparisonKey(characterType, left.s) == characterComparisonKey(characterType, right.s)), nil
}

func outerRelationValue(name string, outer *outerRelationScope) (exprValue, error) {
	if outer == nil {
		return exprValue{}, sqlFailure{1054, "42S22", "Unknown column '" + name + "'"}
	}
	index, err := resolveRelationColumn(name, outer.columns)
	if err != nil {
		if outer.parent != nil {
			return outerRelationValue(name, outer.parent)
		}
		return exprValue{}, err
	}
	return relationColumnValue(outer.columns, index, outer.row)
}

func expressionValueFromMetadata(raw string, metadata columnMetadata) (exprValue, error) {
	if isNumericWireType(metadata.typ) {
		return evaluateScalar(raw)
	}
	return stringValue(raw), nil
}

func isNumericWireType(typ byte) bool {
	switch typ {
	case mysqlTypeTiny, mysqlTypeShort, mysqlTypeLong, mysqlTypeLongLong, mysqlTypeInt24, mysqlTypeFloat, mysqlTypeDouble, mysqlTypeNewDecimal, mysqlTypeBit:
		return true
	default:
		return false
	}
}

func parseAndMaterializeCTEs(context *composedQueryContext, query string, _ *outerRelationScope) (*composedQueryContext, string, error) {
	if !strings.HasPrefix(strings.ToLower(query), "with ") {
		return context, query, nil
	}
	if strings.HasPrefix(strings.ToLower(query), "with recursive ") {
		return nil, "", sqlFailure{1235, "42000", "recursive CTEs are not supported in v0.1"}
	}
	local := &composedQueryContext{executor: context.executor, ctes: cloneComposedRelations(context.ctes), depth: context.depth, planning: context.planning, strictScope: context.strictScope}
	localNames := make(map[string]bool)
	rest := strings.TrimSpace(query[len("WITH "):])
	for {
		var err error
		rest, err = parseOneCTE(local, localNames, rest)
		if err != nil {
			return nil, "", err
		}
		if !strings.HasPrefix(rest, ",") {
			return local, rest, nil
		}
		rest = strings.TrimSpace(rest[1:])
	}
}

func parseOneCTE(context *composedQueryContext, localNames map[string]bool, text string) (string, error) {
	name, rest, err := parseCTEName(localNames, text)
	if err != nil {
		return "", err
	}
	query, remainder, err := parseCTEQuery(rest)
	if err != nil {
		return "", err
	}
	key := catalog.Key(name)
	context.ctes[key] = composedRelation{name: name, query: query, reason: "cte", ctes: cloneComposedRelations(context.ctes), state: &composedRelationState{}}
	localNames[key] = true
	return remainder, nil
}

func parseCTEName(localNames map[string]bool, text string) (string, string, error) {
	name, rest, ok := consumeIdentifier(text)
	if !ok {
		return "", "", sqlFailure{1064, "42000", "malformed CTE name"}
	}
	if localNames[catalog.Key(name)] {
		return "", "", sqlFailure{1060, "42S21", "duplicate CTE name '" + name + "'"}
	}
	return name, strings.TrimSpace(rest), nil
}

func parseCTEQuery(text string) (string, string, error) {
	if strings.HasPrefix(text, "(") {
		return "", "", sqlFailure{1235, "42000", "CTE column lists are not supported in v0.1"}
	}
	if !strings.HasPrefix(strings.ToLower(text), "as ") {
		return "", "", sqlFailure{1064, "42000", "CTE requires AS"}
	}
	text = strings.TrimSpace(text[len("AS "):])
	if !strings.HasPrefix(text, "(") {
		return "", "", sqlFailure{1064, "42000", "CTE query must be parenthesized"}
	}
	close, found := matchingParenthesis(text, 0)
	if !found {
		return "", "", sqlFailure{1064, "42000", "unterminated CTE query"}
	}
	return strings.TrimSpace(text[1:close]), strings.TrimSpace(text[close+1:]), nil
}

func materializeCTE(context *composedQueryContext, key string, relation composedRelation) (composedRelation, error) {
	if relation.result != nil {
		return relation, nil
	}
	if relation.state != nil && relation.state.result != nil {
		relation.result = relation.state.result
		context.ctes[key] = relation
		return relation, nil
	}
	child, err := context.child()
	if err != nil {
		return composedRelation{}, err
	}
	child.ctes = relation.ctes
	var result *queryResult
	if context.planning {
		result, err = describeComposedSelect(child, relation.query, nil)
	} else {
		result, err = executeComposedSelect(child, relation.query, nil)
	}
	if err != nil {
		return composedRelation{}, err
	}
	result, err = materializeQueryResult(result)
	if err != nil {
		return composedRelation{}, err
	}
	relation.result = result
	if relation.state != nil {
		relation.state.result = result
	}
	context.ctes[key] = relation
	return relation, nil
}

func cloneComposedRelations(source map[string]composedRelation) map[string]composedRelation {
	copy := make(map[string]composedRelation, len(source))
	for key, relation := range source {
		copy[key] = relation
	}
	return copy
}

func materializeQueryResult(result *queryResult) (*queryResult, error) {
	if result == nil || result.stream == nil {
		return result, nil
	}
	rows := make([][]string, 0)
	nulls := make([][]bool, 0)
	if err := result.stream(func(row []string, rowNulls []bool) error {
		rows = append(rows, append([]string(nil), row...))
		nulls = append(nulls, append([]bool(nil), rowNulls...))
		return nil
	}); err != nil {
		return nil, err
	}
	return &queryResult{columns: result.columns, rows: rows, nulls: nulls, metadata: result.metadata}, nil
}

func parseSetQuery(query string) (setQuery, bool, error) {
	positions := topLevelSetOperations(query)
	if len(positions) == 0 {
		return setQuery{}, false, nil
	}
	body, order, limit, err := splitSetTail(query, positions[0])
	if err != nil {
		return setQuery{}, false, err
	}
	positions = topLevelSetOperations(body)
	parsed := setQuery{order: order, limit: limit}
	start := 0
	for _, position := range positions {
		term := strings.TrimSpace(body[start:position.start])
		if term == "" {
			return setQuery{}, false, sqlFailure{1064, "42000", "empty set-operation term"}
		}
		parsed.terms = append(parsed.terms, term)
		parsed.operations = append(parsed.operations, position.operation)
		start = position.end
	}
	parsed.terms = append(parsed.terms, strings.TrimSpace(body[start:]))
	return parsed, true, nil
}

type setOperationPosition struct {
	start, end int
	operation  setQueryOperation
}

func topLevelSetOperations(query string) []setOperationPosition {
	positions := make([]setOperationPosition, 0, 2)
	state := queryScanState{}
	queryLength := len(query)
	for index := 0; index < queryLength; index++ {
		index = state.advance(query, index)
		if state.depth != 0 || state.quote != 0 {
			continue
		}
		for _, keyword := range []string{"UNION", "INTERSECT", "EXCEPT"} {
			if !keywordAtIndex(query, index, keyword) {
				continue
			}
			end := index + len(keyword)
			all := false
			next := skipSQLSpace(query, end)
			if keywordAtIndex(query, next, "ALL") {
				all, end = true, next+len("ALL")
			} else if keywordAtIndex(query, next, "DISTINCT") {
				end = next + len("DISTINCT")
			}
			positions = append(positions, setOperationPosition{start: index, end: end, operation: setQueryOperation{kind: setOperationKind(strings.ToLower(keyword)), all: all}})
			index = end - 1
			break
		}
	}
	return positions
}

type queryScanState struct {
	depth int
	quote byte
}

func (state *queryScanState) advance(value string, index int) int {
	character := value[index]
	if state.quote != 0 {
		return state.advanceQuoted(value, index, character)
	}
	if isQueryQuote(character) {
		state.quote = character
		return index
	}
	state.advanceParenthesis(character)
	return index
}

func (state *queryScanState) advanceQuoted(value string, index int, character byte) int {
	if character != state.quote {
		return index
	}
	if index+1 < len(value) && value[index+1] == state.quote {
		return index + 1
	}
	state.quote = 0
	return index
}

func isQueryQuote(character byte) bool {
	return character == '\'' || character == '`' || character == '"'
}

func (state *queryScanState) advanceParenthesis(character byte) {
	if character == '(' {
		state.depth++
		return
	}
	if character == ')' && state.depth > 0 {
		state.depth--
	}
}

func keywordAtIndex(value string, index int, keyword string) bool {
	if index < 0 || index+len(keyword) > len(value) || !strings.EqualFold(value[index:index+len(keyword)], keyword) {
		return false
	}
	return relationWordBoundary(value, index, len(keyword))
}

func skipSQLSpace(value string, index int) int {
	valueLength := len(value)
	for index < valueLength && isRelationSpace(value[index]) {
		index++
	}
	return index
}

func splitSetTail(query string, firstOperation setOperationPosition) (string, string, string, error) {
	orderAt, limitAt := topLevelKeywordAfter(query, "ORDER", firstOperation.end), topLevelKeywordAfter(query, "LIMIT", firstOperation.end)
	tailAt := firstNonNegative(orderAt, limitAt)
	if tailAt < 0 {
		return query, "", "", nil
	}
	body, tail := strings.TrimSpace(query[:tailAt]), strings.TrimSpace(query[tailAt:])
	if orderAt == tailAt {
		if !strings.HasPrefix(strings.ToLower(tail), "order by ") {
			return "", "", "", sqlFailure{1064, "42000", "ORDER requires BY"}
		}
		rest := strings.TrimSpace(tail[len("ORDER BY "):])
		limit := keywordAt(rest, "limit")
		if limit < 0 {
			return body, rest, "", nil
		}
		return body, strings.TrimSpace(rest[:limit]), strings.TrimSpace(rest[limit+len("limit"):]), nil
	}
	return body, "", strings.TrimSpace(tail[len("LIMIT "):]), nil
}

func topLevelKeywordAfter(value, keyword string, after int) int {
	state := queryScanState{}
	valueLength := len(value)
	for index := 0; index < valueLength; index++ {
		index = state.advance(value, index)
		if index < after || state.depth != 0 || state.quote != 0 {
			continue
		}
		if keywordAtIndex(value, index, keyword) {
			return index
		}
	}
	return -1
}

func firstNonNegative(left, right int) int {
	if left < 0 {
		return right
	}
	if right < 0 || left < right {
		return left
	}
	return right
}

func executeSetQuery(context *composedQueryContext, query setQuery, outer *outerRelationScope) (*queryResult, error) {
	results := make([]*queryResult, len(query.terms))
	for index, term := range query.terms {
		result, err := executeComposedSelect(context, term, outer)
		if err != nil {
			return nil, err
		}
		results[index], err = materializeQueryResult(result)
		if err != nil {
			return nil, err
		}
	}
	result, err := reduceSetResults(results, append([]setQueryOperation(nil), query.operations...))
	if err != nil {
		return nil, err
	}
	if err := orderSetResult(result, query.order); err != nil {
		return nil, err
	}
	limit, err := parseRelationalLimit(query.limit)
	if err != nil {
		return nil, err
	}
	applySetLimit(result, limit)
	return result, nil
}

func reduceSetResults(results []*queryResult, operations []setQueryOperation) (*queryResult, error) {
	var err error
	operationCount := len(operations)
	for index := 0; index < operationCount; {
		if operations[index].kind != setIntersect {
			index++
			continue
		}
		if err := validateSetArity(results[index], results[index+1]); err != nil {
			return nil, err
		}
		results[index], err = applySetOperation(results[index], results[index+1], operations[index])
		if err != nil {
			return nil, err
		}
		results, operations = removeSetRightInput(results, operations, index)
		operationCount--
	}
	result := results[0]
	for index, operation := range operations {
		if err := validateSetArity(result, results[index+1]); err != nil {
			return nil, err
		}
		result, err = applySetOperation(result, results[index+1], operation)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func removeSetRightInput(results []*queryResult, operations []setQueryOperation, index int) ([]*queryResult, []setQueryOperation) {
	results = append(results[:index+1], results[index+2:]...)
	operations = append(operations[:index], operations[index+1:]...)
	return results, operations
}

func validateSetArity(left, right *queryResult) error {
	if len(left.columns) != len(right.columns) {
		return sqlFailure{1222, "21000", "used SELECT statements have a different number of columns"}
	}
	return nil
}

func applySetOperation(left, right *queryResult, operation setQueryOperation) (*queryResult, error) {
	metadata, err := reconcileSetMetadata(left, right)
	if err != nil {
		return nil, err
	}
	result := &queryResult{columns: append([]string(nil), left.columns...), metadata: metadata}
	switch operation.kind {
	case setUnion:
		appendResultRows(result, left)
		appendResultRows(result, right)
		if !operation.all {
			deduplicateSetRows(result)
		}
	case setIntersect:
		selectSetRows(result, left, right, operation.all, true)
	case setExcept:
		selectSetRows(result, left, right, operation.all, false)
	}
	return result, nil
}

func reconcileSetMetadata(left, right *queryResult) ([]columnMetadata, error) {
	metadata := make([]columnMetadata, len(left.columns))
	for index, name := range left.columns {
		leftColumn := resultColumnDefinition(name, index, left.metadata)
		rightColumn := resultColumnDefinition(right.columns[index], index, right.metadata)
		common, err := commonSetColumnMetadata(leftColumn, rightColumn)
		if err != nil {
			return nil, err
		}
		metadata[index] = common
	}
	return metadata, nil
}

func commonSetColumnMetadata(left, right columnMetadata) (columnMetadata, error) {
	result := left
	result = reconcileSetDimensions(result, right)
	if left.typ == mysqlTypeNull {
		return rightSetMetadata(result, right), nil
	}
	if right.typ == mysqlTypeNull {
		return result, nil
	}
	return compatibleSetMetadata(result, left, right)
}

func compatibleSetMetadata(result, left, right columnMetadata) (columnMetadata, error) {
	if isNumericWireType(left.typ) && isNumericWireType(right.typ) {
		return compatibleNumericSetMetadata(result, left, right)
	}
	if isCharacterWireType(left.typ) && isCharacterWireType(right.typ) {
		return compatibleCharacterSetMetadata(result, left, right)
	}
	if dateAndDatetime(left.typ, right.typ) {
		result.typ = mysqlTypeDatetime
		return result, nil
	}
	if left.typ == right.typ {
		return result, nil
	}
	return columnMetadata{}, strictConversionError()
}

func compatibleNumericSetMetadata(result, left, right columnMetadata) (columnMetadata, error) {
	if isApproximateWireType(left.typ) != isApproximateWireType(right.typ) || left.flags&mysqlUnsignedFlag != right.flags&mysqlUnsignedFlag {
		return columnMetadata{}, strictConversionError()
	}
	return numericSetMetadata(result, left, right), nil
}

func compatibleCharacterSetMetadata(result, left, right columnMetadata) (columnMetadata, error) {
	leftCoercibility := setCollationCoercibility(left)
	rightCoercibility := setCollationCoercibility(right)
	if left.characterSet == right.characterSet {
		result = characterSetMetadata(result, left.characterSet)
		result.coercibility = min(leftCoercibility, rightCoercibility)
		return result, nil
	}
	if left.characterSet == mysqlCharsetBinary || right.characterSet == mysqlCharsetBinary {
		return columnMetadata{}, strictConversionError()
	}
	if leftCoercibility < rightCoercibility {
		result = characterSetMetadata(result, left.characterSet)
		result.coercibility = leftCoercibility
		return result, nil
	}
	if rightCoercibility < leftCoercibility {
		result = characterSetMetadata(result, right.characterSet)
		result.coercibility = rightCoercibility
		return result, nil
	}
	return columnMetadata{}, strictConversionError()
}

func setCollationCoercibility(metadata columnMetadata) byte {
	if metadata.coercibility != 0 {
		return metadata.coercibility
	}
	if metadata.table != "" {
		return 2
	}
	return 4
}

func dateAndDatetime(left, right byte) bool {
	return left == mysqlTypeDate && right == mysqlTypeDatetime || left == mysqlTypeDatetime && right == mysqlTypeDate
}

func reconcileSetDimensions(result, right columnMetadata) columnMetadata {
	result.length = max(result.length, right.length)
	result.decimals = max(result.decimals, right.decimals)
	if right.flags&mysqlNotNullFlag == 0 || right.typ == mysqlTypeNull || result.typ == mysqlTypeNull {
		result.flags &^= mysqlNotNullFlag
	}
	return result
}

func rightSetMetadata(leftIdentity, right columnMetadata) columnMetadata {
	name, originalName := leftIdentity.name, leftIdentity.originalName
	right.name, right.originalName = name, originalName
	right.flags &^= mysqlNotNullFlag
	return right
}

func numericSetMetadata(result, left, right columnMetadata) columnMetadata {
	result.characterSet, result.flags = mysqlCharsetBinary, result.flags|mysqlBinaryFlag
	if left.flags&mysqlUnsignedFlag == 0 || right.flags&mysqlUnsignedFlag == 0 {
		result.flags &^= mysqlUnsignedFlag
	}
	if isApproximateWireType(left.typ) || isApproximateWireType(right.typ) {
		result.typ, result.length, result.decimals = mysqlTypeDouble, 22, 0
		return result
	}
	if left.typ == mysqlTypeNewDecimal || right.typ == mysqlTypeNewDecimal {
		result.typ = mysqlTypeNewDecimal
		return result
	}
	result.typ, result.length, result.decimals = mysqlTypeLongLong, max(uint32(20), result.length), 0
	return result
}

func characterSetMetadata(result columnMetadata, characterSet uint16) columnMetadata {
	result.typ = mysqlTypeVarString
	if characterSet == mysqlCharsetBinary {
		result.characterSet, result.flags = mysqlCharsetBinary, result.flags|mysqlBinaryFlag
		return result
	}
	if characterSet == mysqlCharsetUTF8MB4Bin {
		result.characterSet = mysqlCharsetUTF8MB4Bin
		return result
	}
	result.characterSet = mysqlCharsetUTF8MB40900AICI
	return result
}

func isApproximateWireType(typ byte) bool { return typ == mysqlTypeFloat || typ == mysqlTypeDouble }

func isCharacterWireType(typ byte) bool {
	switch typ {
	case mysqlTypeVarchar, mysqlTypeVarString, mysqlTypeString, mysqlTypeBlob, mysqlTypeTinyBlob, mysqlTypeMediumBlob, mysqlTypeLongBlob:
		return true
	default:
		return false
	}
}

func appendResultRows(target, source *queryResult) {
	for index, row := range source.rows {
		target.rows = append(target.rows, normalizeSetRow(row, resultNullRow(source, index), target.metadata))
		target.nulls = append(target.nulls, resultNullRow(source, index))
	}
}

func normalizeSetRow(row []string, nulls []bool, metadata []columnMetadata) []string {
	normalized := append([]string(nil), row...)
	for index := range normalized {
		if index < len(nulls) && nulls[index] {
			continue
		}
		normalized[index] = normalizeSetValue(normalized[index], metadata[index])
	}
	return normalized
}

func normalizeSetValue(raw string, metadata columnMetadata) string {
	if metadata.typ == mysqlTypeDatetime && len(raw) == len("2000-01-01") {
		return raw + " 00:00:00"
	}
	if !isNumericWireType(metadata.typ) {
		return raw
	}
	value, err := evaluateScalar(raw)
	if err != nil {
		return raw
	}
	if metadata.typ == mysqlTypeDouble {
		return renderDouble(toFloat(value))
	}
	if metadata.typ == mysqlTypeNewDecimal {
		decimal := toDecimal(value)
		if int(metadata.decimals) >= decimal.scale {
			decimal = decimalValue{unscaled: decimal.rescaled(int(metadata.decimals)), scale: int(metadata.decimals)}
		}
		return decimal.renderDecimal()
	}
	return value.render()
}

func resultNullRow(result *queryResult, index int) []bool {
	if index >= len(result.nulls) {
		return make([]bool, len(result.columns))
	}
	return append([]bool(nil), result.nulls[index]...)
}

func deduplicateSetRows(result *queryResult) {
	seen := make(map[string]bool, len(result.rows))
	rows, nulls := result.rows[:0], result.nulls[:0]
	for index, row := range result.rows {
		key := setResultRowKey(row, resultNullRow(result, index), result.metadata)
		if seen[key] {
			continue
		}
		seen[key] = true
		rows, nulls = append(rows, row), append(nulls, resultNullRow(result, index))
	}
	result.rows, result.nulls = rows, nulls
}

func selectSetRows(target, left, right *queryResult, all, intersection bool) {
	rightCounts := make(map[string]int, len(right.rows))
	for index, row := range right.rows {
		rightCounts[setResultRowKey(row, resultNullRow(right, index), target.metadata)]++
	}
	emitted := make(map[string]bool)
	for index, row := range left.rows {
		nulls := resultNullRow(left, index)
		key := setResultRowKey(row, nulls, target.metadata)
		if !includeSetRow(key, rightCounts, emitted, all, intersection) {
			continue
		}
		emitted[key] = true
		target.rows = append(target.rows, normalizeSetRow(row, nulls, target.metadata))
		target.nulls = append(target.nulls, nulls)
	}
}

func includeSetRow(key string, rightCounts map[string]int, emitted map[string]bool, all, intersection bool) bool {
	match := rightCounts[key] > 0
	if all && match {
		rightCounts[key]--
		return intersection
	}
	if all {
		return !intersection
	}
	return match == intersection && !emitted[key]
}

func setResultRowKey(row []string, nulls []bool, metadata []columnMetadata) string {
	var builder strings.Builder
	for index, value := range row {
		if index < len(nulls) && nulls[index] {
			builder.WriteString("N;")
			continue
		}
		value = setComparisonKey(value, metadata[index])
		builder.WriteString(strconv.Itoa(len(value)))
		builder.WriteByte(':')
		builder.WriteString(value)
		builder.WriteByte(';')
	}
	return builder.String()
}

func setComparisonKey(value string, metadata columnMetadata) string {
	if isNumericWireType(metadata.typ) {
		normalized := normalizeSetValue(value, metadata)
		if metadata.typ == mysqlTypeNewDecimal {
			decimal, ok := parseDecimalText(normalized)
			if ok {
				for decimal.scale > 0 {
					quotient, remainder := new(big.Int), new(big.Int)
					quotient.QuoRem(decimal.unscaled, big.NewInt(10), remainder)
					if remainder.Sign() != 0 {
						break
					}
					decimal.unscaled, decimal.scale = quotient, decimal.scale-1
				}
				return decimal.renderDecimal()
			}
		}
		return normalized
	}
	if isCharacterWireType(metadata.typ) {
		characterType := defaultStringType
		if metadata.characterSet == mysqlCharsetUTF8MB4Bin {
			characterType.collation = collationBin
		}
		return characterComparisonKey(characterType, value)
	}
	return value
}

func orderSetResult(result *queryResult, orderText string) error {
	if strings.TrimSpace(orderText) == "" {
		return nil
	}
	type setOrder struct {
		column    int
		direction string
	}
	orders := make([]setOrder, 0)
	for _, item := range splitCSV(orderText) {
		expression, direction := splitOrderDirection(item)
		column, err := setOrderColumn(expression, result.columns)
		if err != nil {
			return err
		}
		orders = append(orders, setOrder{column: column, direction: direction})
	}
	permutation := make([]int, len(result.rows))
	for index := range permutation {
		permutation[index] = index
	}
	sort.SliceStable(permutation, func(left, right int) bool {
		for _, order := range orders {
			comparison := compareSetValues(result, permutation[left], permutation[right], order.column)
			if comparison == 0 {
				continue
			}
			return orderedBefore(comparison, order.direction)
		}
		return false
	})
	reorderSetResult(result, permutation)
	return nil
}

func reorderSetResult(result *queryResult, permutation []int) {
	rows := make([][]string, len(permutation))
	nulls := make([][]bool, len(permutation))
	for index, source := range permutation {
		rows[index] = result.rows[source]
		nulls[index] = resultNullRow(result, source)
	}
	result.rows, result.nulls = rows, nulls
}

func setOrderColumn(expression string, columns []string) (int, error) {
	if ordinal, err := strconv.Atoi(expression); err == nil {
		if ordinal > 0 && ordinal <= len(columns) {
			return ordinal - 1, nil
		}
		return 0, sqlFailure{1054, "42S22", "Unknown column '" + expression + "' in 'order clause'"}
	}
	found := -1
	for index, name := range columns {
		if identifiersEqual(name, expression) {
			if found >= 0 {
				return 0, sqlFailure{1052, "23000", "Column '" + expression + "' in order clause is ambiguous"}
			}
			found = index
		}
	}
	if found < 0 {
		return 0, sqlFailure{1054, "42S22", "Unknown column '" + expression + "' in 'order clause'"}
	}
	return found, nil
}

func compareSetValues(result *queryResult, left, right, column int) int {
	leftNull := resultValueIsNull(left, column, result.nulls)
	rightNull := resultValueIsNull(right, column, result.nulls)
	if leftNull || rightNull {
		if leftNull == rightNull {
			return 0
		}
		if leftNull {
			return -1
		}
		return 1
	}
	return compareNonNullSetValues(result, left, right, column)
}

func compareNonNullSetValues(result *queryResult, left, right, column int) int {
	metadata := resultColumnDefinition(result.columns[column], column, result.metadata)
	leftValue, leftErr := expressionValueFromMetadata(result.rows[left][column], metadata)
	rightValue, rightErr := expressionValueFromMetadata(result.rows[right][column], metadata)
	if leftErr == nil && rightErr == nil {
		if leftValue.kind == valueString && rightValue.kind == valueString {
			return strings.Compare(setComparisonKey(leftValue.s, metadata), setComparisonKey(rightValue.s, metadata))
		}
		if comparison, err := compareOperands(leftValue, rightValue); err == nil {
			return comparison
		}
	}
	return strings.Compare(result.rows[left][column], result.rows[right][column])
}

func applySetLimit(result *queryResult, limit relationalLimit) {
	if !limit.present {
		return
	}
	start := min(limit.offset, len(result.rows))
	end := min(start+limit.count, len(result.rows))
	result.rows, result.nulls = result.rows[start:end], result.nulls[start:end]
}

func stripWholeQueryParentheses(query string) string {
	for strings.HasPrefix(query, "(") {
		close, ok := matchingParenthesis(query, 0)
		if !ok || close != len(query)-1 {
			return query
		}
		query = strings.TrimSpace(query[1:close])
	}
	return query
}

func isComposedSelectStatement(query string) bool {
	query = strings.TrimSpace(query)
	if strings.HasPrefix(query, "(") && len(topLevelSetOperations(query)) > 0 {
		return true
	}
	query = stripWholeQueryParentheses(query)
	lower := strings.ToLower(query)
	return strings.HasPrefix(lower, "select ") || strings.HasPrefix(lower, "with ")
}

func scalarSubquerySQL(expression string) (string, bool) {
	expression = strings.TrimSpace(expression)
	if !strings.HasPrefix(expression, "(") {
		return "", false
	}
	close, ok := matchingParenthesis(expression, 0)
	if !ok || close != len(expression)-1 {
		return "", false
	}
	query := strings.TrimSpace(expression[1:close])
	lower := strings.ToLower(query)
	return query, strings.HasPrefix(lower, "select ") || strings.HasPrefix(lower, "with ")
}

func isScalarSubqueryExpression(expression string) bool {
	_, ok := scalarSubquerySQL(expression)
	return ok
}

func queryResultTable(name string, result *queryResult) (catalog.Table, error) {
	result, err := materializeQueryResult(result)
	if err != nil {
		return catalog.Table{}, err
	}
	table := catalog.Table{Name: name, Columns: append([]string(nil), result.columns...), Rows: make([][]string, len(result.rows)), ColumnTypes: make([]string, len(result.columns))}
	for index, row := range result.rows {
		table.Rows[index] = append([]string(nil), row...)
		for column := range table.Columns {
			if resultValueIsNull(index, column, result.nulls) {
				table.Rows[index][column] = storedSQLNullValue
			}
		}
	}
	for index, column := range result.columns {
		table.ColumnTypes[index] = metadataTypeName(resultColumnDefinition(column, index, result.metadata))
	}
	return table, nil
}

var composedWireTypeNames = map[byte]string{
	mysqlTypeTiny:       "TINYINT",
	mysqlTypeShort:      "SMALLINT",
	mysqlTypeLong:       "INT",
	mysqlTypeInt24:      "INT",
	mysqlTypeLongLong:   "BIGINT",
	mysqlTypeFloat:      "FLOAT",
	mysqlTypeDouble:     "DOUBLE",
	mysqlTypeDate:       "DATE",
	mysqlTypeDatetime:   "DATETIME",
	mysqlTypeTimestamp:  "TIMESTAMP",
	mysqlTypeTime:       "TIME",
	mysqlTypeYear:       "YEAR",
	mysqlTypeBit:        "BIT(64)",
	mysqlTypeBlob:       "BLOB",
	mysqlTypeTinyBlob:   "BLOB",
	mysqlTypeMediumBlob: "BLOB",
	mysqlTypeLongBlob:   "BLOB",
}

func metadataTypeName(metadata columnMetadata) string {
	if metadata.typ == mysqlTypeNewDecimal {
		return "DECIMAL(65," + strconv.Itoa(int(metadata.decimals)) + ")"
	}
	if name, found := composedWireTypeNames[metadata.typ]; found {
		if metadata.flags&mysqlUnsignedFlag != 0 && isNumericWireType(metadata.typ) {
			name += " UNSIGNED"
		}
		return name
	}
	length := metadata.length
	if length == 0 {
		length = 255
	}
	if metadata.characterSet == mysqlCharsetBinary {
		return "VARBINARY(" + strconv.FormatUint(uint64(length), 10) + ")"
	}
	length = max(uint32(1), length/4)
	typeName := "VARCHAR(" + strconv.FormatUint(uint64(length), 10) + ")"
	if metadata.characterSet == mysqlCharsetUTF8MB4Bin {
		typeName += " COLLATE utf8mb4_bin"
	}
	return typeName
}
