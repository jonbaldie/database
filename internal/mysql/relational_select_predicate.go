package mysql

import (
	"strings"
)

type relationPredicate func(relationRow) (exprValue, error)

type relationOperand struct {
	column     int
	isColumn   bool
	computed   bool
	raw        string
	value      exprValue
	definition relationColumn
	columns    []relationColumn
}

func compileRelationPredicate(text string, columns []relationColumn, session *session) (relationPredicate, error) {
	text = stripRelationParentheses(strings.TrimSpace(text))
	if text == "" {
		return nil, unsupportedExpression()
	}
	if parts := splitRelationKeyword(text, "OR"); len(parts) > 1 {
		return combineRelationPredicates(parts, columns, session, logicalOr)
	}
	if parts := splitRelationKeyword(text, "AND"); len(parts) > 1 {
		return combineRelationPredicates(parts, columns, session, logicalAnd)
	}
	if strings.HasPrefix(strings.ToLower(text), "not ") {
		return compileNegatedPredicate(text, columns, session)
	}
	return compileSimpleRelationPredicate(text, columns, session)
}

func compileNegatedPredicate(text string, columns []relationColumn, session *session) (relationPredicate, error) {
	inner, err := compileRelationPredicate(strings.TrimSpace(text[len("not "):]), columns, session)
	if err != nil {
		return nil, err
	}
	return func(row relationRow) (exprValue, error) {
		value, err := inner(row)
		if err != nil {
			return exprValue{}, err
		}
		return logicalNot(value)
	}, nil
}

func compileSimpleRelationPredicate(text string, columns []relationColumn, session *session) (relationPredicate, error) {
	if operator, left, right, ok := findRelationComparison(text); ok {
		return compileRelationComparison(operator, left, right, columns, session)
	}
	if isPosition, left, right := splitRelationKeywordOnce(text, "IS"); isPosition {
		return compileIsPredicate(left, right, columns, session)
	}
	operand, err := compileRelationOperand(text, columns, session)
	if err != nil {
		return nil, err
	}
	return func(row relationRow) (exprValue, error) {
		return relationOperandValue(operand, row)
	}, nil
}

func compileIsPredicate(left, right string, columns []relationColumn, session *session) (relationPredicate, error) {
	operand, err := compileRelationOperand(left, columns, session)
	if err != nil {
		return nil, err
	}
	negate := false
	right = strings.TrimSpace(right)
	if strings.HasPrefix(strings.ToLower(right), "not ") {
		negate, right = true, strings.TrimSpace(right[len("not "):])
	}
	switch strings.ToLower(right) {
	case "null", "unknown":
		return relationNullPredicate(operand, negate), nil
	case "true", "false":
		return relationTruthPredicate(operand, strings.EqualFold(right, "true"), negate), nil
	default:
		return nil, unsupportedExpression()
	}
}

func relationNullPredicate(operand relationOperand, negate bool) relationPredicate {
	return func(row relationRow) (exprValue, error) {
		value, err := relationOperandValue(operand, row)
		if err != nil {
			return exprValue{}, err
		}
		return isNullPredicate(value, negate), nil
	}
}

func relationTruthPredicate(operand relationOperand, wantTrue, negate bool) relationPredicate {
	return func(row relationRow) (exprValue, error) {
		value, err := relationOperandValue(operand, row)
		if err != nil {
			return exprValue{}, err
		}
		return isTruthPredicate(value, wantTrue, negate)
	}
}

func combineRelationPredicates(parts []string, columns []relationColumn, session *session, combine func(exprValue, exprValue) (exprValue, error)) (relationPredicate, error) {
	predicates := make([]relationPredicate, len(parts))
	for index, part := range parts {
		predicate, err := compileRelationPredicate(part, columns, session)
		if err != nil {
			return nil, err
		}
		predicates[index] = predicate
	}
	return func(row relationRow) (exprValue, error) {
		value, err := predicates[0](row)
		if err != nil {
			return exprValue{}, err
		}
		for _, predicate := range predicates[1:] {
			right, rightErr := predicate(row)
			if rightErr != nil {
				return exprValue{}, rightErr
			}
			value, err = combine(value, right)
			if err != nil {
				return exprValue{}, err
			}
		}
		return value, nil
	}, nil
}

func compileRelationComparison(operator, left, right string, columns []relationColumn, session *session) (relationPredicate, error) {
	leftOperand, err := compileRelationOperand(left, columns, session)
	if err != nil {
		return nil, err
	}
	rightOperand, err := compileRelationOperand(right, columns, session)
	if err != nil {
		return nil, err
	}
	if err := rejectQuotedNumericComparison(operator, leftOperand, rightOperand); err != nil {
		return nil, err
	}
	leftOperand, rightOperand, err = coerceRelationLiterals(leftOperand, rightOperand, columns, session)
	if err != nil {
		return nil, err
	}
	return relationComparisonPredicate(operator, leftOperand, rightOperand), nil
}

func rejectQuotedNumericComparison(operator string, left, right relationOperand) error {
	if operator == "=" {
		return nil
	}
	if quotedNumericRight(left, right) {
		return strictConversionError()
	}
	if quotedNumericLeft(left, right) {
		return strictConversionError()
	}
	return nil
}

func quotedNumericRight(left, right relationOperand) bool {
	return left.isColumn && !right.isColumn && quotedRelationLiteral(right.raw) && relationColumnIsNumeric(left.definition)
}

func quotedNumericLeft(left, right relationOperand) bool {
	return right.isColumn && !left.isColumn && quotedRelationLiteral(left.raw) && relationColumnIsNumeric(right.definition)
}

func relationComparisonPredicate(operator string, leftOperand, rightOperand relationOperand) relationPredicate {
	return func(row relationRow) (exprValue, error) {
		leftValue, err := relationOperandValue(leftOperand, row)
		if err != nil {
			return exprValue{}, err
		}
		rightValue, err := relationOperandValue(rightOperand, row)
		if err != nil {
			return exprValue{}, err
		}
		return compareRelationValues(operator, leftOperand, rightOperand, leftValue, rightValue)
	}
}

func compareRelationValues(operator string, leftOperand, rightOperand relationOperand, left, right exprValue) (exprValue, error) {
	if operator == "<=>" {
		if left.isNull() || right.isNull() {
			return boolValue(left.isNull() && right.isNull()), nil
		}
		order, err := compareRelationOperands(leftOperand, rightOperand, left, right)
		if err != nil {
			return exprValue{}, err
		}
		return boolValue(order == 0), nil
	}
	if left.isNull() || right.isNull() {
		return nullValue(), nil
	}
	order, err := compareRelationOperands(leftOperand, rightOperand, left, right)
	if err != nil {
		return exprValue{}, err
	}
	return boolValue(applyComparison(operator, order)), nil
}

func compareRelationOperands(leftOperand, rightOperand relationOperand, left, right exprValue) (int, error) {
	if left.kind == valueString && right.kind == valueString {
		typ, found, err := relationCharacterComparisonType(leftOperand, rightOperand)
		if err != nil {
			return 0, err
		}
		if found {
			return strings.Compare(characterComparisonKey(typ, left.s), characterComparisonKey(typ, right.s)), nil
		}
	}
	return compareOperands(left, right)
}

func relationCharacterComparisonType(left, right relationOperand) (characterType, bool, error) {
	var selected characterType
	found := false
	for _, operand := range []relationOperand{left, right} {
		if !operand.isColumn {
			continue
		}
		typ, err := parseCharacterType(operand.definition.typeName)
		if err != nil {
			return characterType{}, false, err
		}
		if typ.kind != characterText {
			continue
		}
		if !found || typ.collation == collationBin {
			selected, found = typ, true
		}
	}
	return selected, found, nil
}

func compileRelationOperand(text string, columns []relationColumn, session *session) (relationOperand, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return relationOperand{}, unsupportedExpression()
	}
	if value, err := evaluateScalar(text); err == nil {
		return relationOperand{raw: text, value: value}, nil
	}
	column, err := resolveRelationColumn(text, columns)
	if err == nil {
		return relationOperand{column: column, isColumn: true, raw: text, definition: columns[column]}, nil
	}
	if _, expressionErr := evaluateRelationExpression(text, columns, sampleRelationRow(columns)); expressionErr != nil {
		return relationOperand{}, expressionErr
	}
	return relationOperand{computed: true, raw: text, columns: columns}, nil
}

func coerceRelationLiterals(left, right relationOperand, columns []relationColumn, session *session) (relationOperand, relationOperand, error) {
	switch {
	case left.isColumn && !right.isColumn:
		value, err := typedRelationLiteral(right, left.definition, session)
		if err != nil {
			return relationOperand{}, relationOperand{}, err
		}
		right.value = value
	case right.isColumn && !left.isColumn:
		value, err := typedRelationLiteral(left, right.definition, session)
		if err != nil {
			return relationOperand{}, relationOperand{}, err
		}
		left.value = value
	}
	return left, right, nil
}

func typedRelationLiteral(operand relationOperand, column relationColumn, session *session) (exprValue, error) {
	if operand.value.isNull() {
		return operand.value, nil
	}
	if column.typeName == "" {
		return stringValue(scalar(operand.raw)), nil
	}
	offset, err := sessionTimeZoneOffset(session)
	if err != nil {
		return exprValue{}, err
	}
	canonical, err := canonicalColumnValueAtOffset(column.tableDefinition, column.index, operand.raw, 1, offset)
	if err != nil {
		return exprValue{}, err
	}
	return relationStoredValue(column, canonical)
}

func relationOperandValue(operand relationOperand, row relationRow) (exprValue, error) {
	if operand.computed {
		return evaluateRelationExpression(operand.raw, operand.columns, row)
	}
	if !operand.isColumn {
		return operand.value, nil
	}
	if operand.column < 0 || operand.column >= len(row.values) {
		return exprValue{}, sqlFailure{1105, "HY000", "row shape does not match SELECT plan"}
	}
	raw := row.values[operand.column]
	if raw == storedSQLNullValue && operand.definition.coalesce >= 0 && operand.definition.coalesce < len(row.values) {
		raw = row.values[operand.definition.coalesce]
	}
	return relationStoredValue(operand.definition, raw)
}

func evaluateRelationExpression(text string, columns []relationColumn, row relationRow) (exprValue, error) {
	return evaluateScalarWithResolver(text, func(name string) (exprValue, error) {
		column, err := resolveRelationColumn(name, columns)
		if err != nil {
			return exprValue{}, err
		}
		return relationColumnValue(columns, column, row)
	})
}

func sampleRelationRow(columns []relationColumn) relationRow {
	values := make([]string, len(columns))
	for index, column := range columns {
		values[index] = sampleRelationValue(column)
	}
	return relationRow{values: values}
}

func sampleRelationValue(column relationColumn) string {
	numeric, numericErr := parseNumericType(column.typeName)
	if numericErr == nil && numeric.kind != numericNone {
		return "1"
	}
	temporal, temporalErr := parseTemporalType(column.typeName)
	if temporalErr == nil {
		switch temporal.kind {
		case temporalDate:
			return "2000-01-01"
		case temporalDatetime, temporalTimestamp:
			return "2000-01-01 00:00:00"
		case temporalTime:
			return "01:00:00"
		case temporalYear:
			return "2000"
		}
	}
	return "x"
}

func relationColumnValue(columns []relationColumn, index int, row relationRow) (exprValue, error) {
	if index < 0 || index >= len(columns) || index >= len(row.values) {
		return exprValue{}, sqlFailure{1105, "HY000", "row shape does not match SELECT plan"}
	}
	column := columns[index]
	raw := row.values[index]
	if raw == storedSQLNullValue && column.coalesce >= 0 && column.coalesce < len(row.values) {
		raw = row.values[column.coalesce]
	}
	return relationStoredValue(column, raw)
}

func relationStoredValue(column relationColumn, raw string) (exprValue, error) {
	if raw == storedSQLNullValue {
		return nullValue(), nil
	}
	if column.typeName != "" {
		typ, err := parseNumericType(column.typeName)
		if err != nil {
			return exprValue{}, err
		}
		if typ.kind != numericNone {
			return evaluateScalar(raw)
		}
	}
	return stringValue(raw), nil
}

func quotedRelationLiteral(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\''
}

func relationColumnIsNumeric(column relationColumn) bool {
	typ, err := parseNumericType(column.typeName)
	return err == nil && typ.kind != numericNone
}

func resolveRelationColumn(text string, columns []relationColumn) (int, error) {
	parts, valid := splitQualifiedIdentifier(strings.TrimSpace(text))
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return 0, unsupportedExpression()
	}
	qualifier, name := "", parts[0]
	if len(parts) == 2 {
		qualifier, name = parts[0], parts[1]
	}
	matched := relationColumnMatches(columns, qualifier, name)
	return resolveRelationColumnMatch(text, matched)
}

func relationColumnMatches(columns []relationColumn, qualifier, name string) []int {
	matched := make([]int, 0, 1)
	for index, column := range columns {
		if relationColumnMatchesName(column, qualifier, name) {
			matched = append(matched, index)
		}
	}
	return matched
}

func relationColumnMatchesName(column relationColumn, qualifier, name string) bool {
	if !identifiersEqual(column.name, name) {
		return false
	}
	if qualifier == "" && column.hidden {
		return false
	}
	return qualifier == "" || identifiersEqual(qualifier, column.qualifier)
}

func resolveRelationColumnMatch(text string, matched []int) (int, error) {
	switch len(matched) {
	case 0:
		return 0, sqlFailure{1054, "42S22", "unknown column '" + text + "'"}
	case 1:
		return matched[0], nil
	default:
		return 0, sqlFailure{1052, "23000", "Column '" + text + "' in field list is ambiguous"}
	}
}

func findNamedColumn(columns []relationColumn, qualifier, name string) (relationColumn, bool) {
	var found relationColumn
	count := 0
	for _, column := range columns {
		if !identifiersEqual(column.name, name) {
			continue
		}
		if qualifier == "" && column.hidden {
			continue
		}
		if qualifier != "" && !identifiersEqual(column.qualifier, qualifier) {
			continue
		}
		found, count = column, count+1
	}
	return found, count == 1
}

func splitRelationKeyword(value, keyword string) []string {
	parts := make([]string, 0, 2)
	start := 0
	length := len(value)
	keywordLength := len(keyword)
	for index := 0; index+1+keywordLength <= length; index++ {
		if value[index] == '\'' {
			index = skipQuoted(value, index)
			continue
		}
		if !relationKeywordCandidate(value, keyword, index) {
			continue
		}
		parts = append(parts, strings.TrimSpace(value[start:index+1]))
		start = index + 1 + keywordLength
		index += keywordLength
	}
	if len(parts) == 0 {
		return []string{strings.TrimSpace(value)}
	}
	parts = append(parts, strings.TrimSpace(value[start:]))
	return parts
}

func relationKeywordCandidate(value, keyword string, index int) bool {
	if !isRelationSpace(value[index]) {
		return false
	}
	start := index + 1
	return strings.EqualFold(value[start:start+len(keyword)], keyword) && relationWordBoundary(value, start, len(keyword)) && relationDepthAt(value, index) == 0
}

func isRelationSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func splitRelationKeywordOnce(value, keyword string) (bool, string, string) {
	parts := splitRelationKeyword(value, keyword)
	if len(parts) != 2 {
		return false, "", ""
	}
	return true, parts[0], parts[1]
}

func relationWordBoundary(value string, index, length int) bool {
	before := index == 0 || !isRelationWordByte(value[index-1])
	after := index+length == len(value) || !isRelationWordByte(value[index+length])
	return before && after
}

func isRelationWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func relationDepthAt(value string, end int) int {
	state := relationDepthState{}
	length := minRelationLength(end, len(value))
	for index := 0; index < length; index++ {
		index = state.advance(value, index, length)
	}
	return state.depth
}

type relationDepthState struct {
	depth  int
	quoted bool
}

func minRelationLength(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (state *relationDepthState) advance(value string, index, length int) int {
	if value[index] == '\'' {
		if state.quoted && index+1 < length && value[index+1] == '\'' {
			return index + 1
		}
		state.quoted = !state.quoted
		return index
	}
	if state.quoted {
		return index
	}
	switch value[index] {
	case '(':
		state.depth++
	case ')':
		if state.depth > 0 {
			state.depth--
		}
	}
	return index
}

func findRelationComparison(value string) (string, string, string, bool) {
	operators := []string{"<=>", "<=", ">=", "<>", "!=", "=", "<", ">"}
	state := relationDepthState{}
	length := len(value)
	for index := 0; index < length; index++ {
		index = state.advance(value, index, length)
		if state.quoted || state.depth != 0 {
			continue
		}
		if operator, left, right, ok := relationOperatorAt(value, index, operators); ok {
			return operator, left, right, true
		}
	}
	return "", "", "", false
}

func relationOperatorAt(value string, index int, operators []string) (string, string, string, bool) {
	for _, operator := range operators {
		if !strings.HasPrefix(value[index:], operator) {
			continue
		}
		left, right := strings.TrimSpace(value[:index]), strings.TrimSpace(value[index+len(operator):])
		return operator, left, right, left != "" && right != ""
	}
	return "", "", "", false
}

func stripRelationParentheses(value string) string {
	for strings.HasPrefix(value, "(") {
		close, ok := matchingParenthesis(value, 0)
		if !ok || strings.TrimSpace(value[close+1:]) != "" {
			break
		}
		value = strings.TrimSpace(value[1:close])
	}
	return value
}
