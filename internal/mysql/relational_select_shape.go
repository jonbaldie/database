package mysql

import (
	"sort"
	"strconv"
	"strings"
)

func parseRelationalProjection(text string, columns []relationColumn, context *composedQueryContext, outer *outerRelationScope) ([]relationalProjection, bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false, sqlFailure{1064, "42000", "empty SELECT list"}
	}
	if text == "*" {
		projections := projectionsForColumns(columns)
		return projections, len(projections) == len(columns), nil
	}
	projections, err := parseProjectionItems(text, columns, context, outer)
	if err != nil {
		return nil, false, err
	}
	return projections, false, nil
}

func parseProjectionItems(text string, columns []relationColumn, context *composedQueryContext, outer *outerRelationScope) ([]relationalProjection, error) {
	projections := make([]relationalProjection, 0)
	for _, item := range splitCSV(text) {
		parsed, err := parseProjectionItem(item, columns, context, outer)
		if err != nil {
			return nil, err
		}
		projections = append(projections, parsed...)
	}
	if len(projections) == 0 {
		return nil, sqlFailure{1064, "42000", "empty SELECT list"}
	}
	return projections, nil
}

func parseProjectionItem(item string, columns []relationColumn, context *composedQueryContext, outer *outerRelationScope) ([]relationalProjection, error) {
	expression, alias, err := splitProjectionAlias(item)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.TrimSpace(expression), ".*") {
		return wildcardProjections(expression, columns)
	}
	if query, ok := scalarSubquerySQL(expression); ok {
		return subqueryProjection(query, expression, alias, columns, context, outer)
	}
	if column, resolveErr := resolveRelationColumn(expression, columns); resolveErr == nil {
		projection := relationProjection(column, alias)
		projection.expression = expression
		return []relationalProjection{projection}, nil
	}
	if projection, scalarErr := scalarProjection(expression, alias); scalarErr == nil {
		return projection, nil
	}
	return computedProjection(expression, alias, columns, outer)
}

func subqueryProjection(query, expression, alias string, columns []relationColumn, context *composedQueryContext, outer *outerRelationScope) ([]relationalProjection, error) {
	scope := &outerRelationScope{columns: columns, row: sampleRelationRow(columns), parent: outer}
	result, err := describeComposedSelect(context, query, scope)
	if err != nil {
		return nil, err
	}
	if len(result.columns) != 1 {
		return nil, sqlFailure{1241, "21000", "operand should contain 1 column"}
	}
	metadata := resultColumnDefinition(result.columns[0], 0, result.metadata)
	correlated := composedQueryIsCorrelated(context, query)
	value := exprValue{}
	if !correlated && !context.planning {
		value, metadata, err = executeScalarSubquery(context, query, nil)
		if err != nil {
			return nil, err
		}
	}
	name := expression
	if alias != "" {
		name = alias
	}
	metadata.name = name
	metadata.flags &^= mysqlNotNullFlag
	return []relationalProjection{{
		expression: expression, name: name, alias: alias, column: -1,
		subquery: query, context: context, metadata: metadata, scalar: !correlated, value: value,
	}}, nil
}

func wildcardProjections(expression string, columns []relationColumn) ([]relationalProjection, error) {
	trimmed := strings.TrimSpace(expression)
	qualifier, valid := singleIdentifier(strings.TrimSpace(trimmed[:len(trimmed)-2]))
	if !valid {
		return nil, sqlFailure{1146, "42S02", "unknown table '" + expression + "'"}
	}
	projections := make([]relationalProjection, 0)
	for index, column := range columns {
		if identifiersEqual(column.qualifier, qualifier) {
			projections = append(projections, relationProjection(index, ""))
		}
	}
	if len(projections) == 0 {
		return nil, sqlFailure{1146, "42S02", "unknown table '" + qualifier + "'"}
	}
	return projections, nil
}

func scalarProjection(expression, alias string) ([]relationalProjection, error) {
	rendered, isNull, metadata, scalarErr := scalarColumn(expression)
	if scalarErr != nil {
		return nil, scalarErr
	}
	value := literalQueryResult{value: rendered, isNull: isNull, metadata: metadata, supported: true}
	name := expression
	if alias != "" {
		name = alias
		value.metadata.originalName = value.metadata.name
	}
	value.metadata.name = name
	return []relationalProjection{{
		expression: expression, name: name, alias: alias, column: -1,
		scalar: true, value: literalExprValue(value), metadata: value.metadata,
	}}, nil
}

func computedProjection(expression, alias string, columns []relationColumn, outer *outerRelationScope) ([]relationalProjection, error) {
	metadata, err := relationExpressionMetadataContext(expression, columns, outer)
	if err != nil {
		return nil, err
	}
	name := expression
	if alias != "" {
		name = alias
	}
	metadata.name = name
	return []relationalProjection{{
		expression: expression, name: name, alias: alias, column: -1,
		computed: true, metadata: metadata, outer: outer,
	}}, nil
}

func projectionsForColumns(columns []relationColumn) []relationalProjection {
	projections := make([]relationalProjection, 0, len(columns))
	for index := range columns {
		if columns[index].hidden {
			continue
		}
		projections = append(projections, relationProjection(index, ""))
	}
	return projections
}

func relationProjection(column int, alias string) relationalProjection {
	return relationalProjection{column: column, alias: alias}
}

func splitProjectionAlias(item string) (string, string, error) {
	item = strings.TrimSpace(item)
	if item == "" {
		return "", "", sqlFailure{1064, "42000", "empty SELECT projection"}
	}
	if found, expression, alias := splitRelationKeywordOnce(item, "AS"); found {
		name, valid := singleIdentifier(alias)
		if !valid {
			return "", "", sqlFailure{1064, "42000", "invalid SELECT alias"}
		}
		if err := validateIdentifierLength(name); err != nil {
			return "", "", err
		}
		return strings.TrimSpace(expression), name, nil
	}
	return item, "", nil
}

func literalExprValue(value literalQueryResult) exprValue {
	if value.isNull {
		return nullValue()
	}
	parsed, err := evaluateScalar(value.value)
	if err == nil {
		return parsed
	}
	return stringValue(value.value)
}

func (p relationalProjection) resolveName(columns []relationColumn) relationalProjection {
	if p.scalar || p.computed || p.subquery != "" {
		return p
	}
	column := columns[p.column]
	name := column.name
	if p.alias != "" {
		name = p.alias
	}
	metadata := column.metadata
	metadata.name = name
	p.name, p.metadata = name, metadata
	return p
}

func (p *relationalSelectPlan) projectRow(row relationRow) (relationalResultRow, error) {
	result := relationalResultRow{
		values: make([]string, len(p.projection)), nulls: make([]bool, len(p.projection)),
		source: row, projections: make([]exprValue, len(p.projection)), orders: make([]exprValue, len(p.order)),
	}
	if err := p.projectValues(row, &result); err != nil {
		return relationalResultRow{}, err
	}
	if err := p.projectOrderValues(row, &result); err != nil {
		return relationalResultRow{}, err
	}
	return result, nil
}

func (p *relationalSelectPlan) projectValues(row relationRow, result *relationalResultRow) error {
	for index, projection := range p.projection {
		projection = projection.resolveName(p.source.columns)
		p.projection[index] = projection
		value, err := p.projectionValue(projection, row)
		if err != nil {
			return err
		}
		result.projections[index] = value
		if value.isNull() {
			result.values[index], result.nulls[index] = storedSQLNullValue, true
		} else {
			result.values[index] = value.render()
		}
	}
	return nil
}

func (p *relationalSelectPlan) projectionValue(projection relationalProjection, row relationRow) (exprValue, error) {
	if projection.subquery != "" && !projection.scalar {
		value, _, err := executeScalarSubquery(projection.context, projection.subquery, &outerRelationScope{columns: p.source.columns, row: row, parent: p.outer})
		return value, err
	}
	if projection.scalar {
		return projection.value, nil
	}
	if projection.computed {
		return evaluateRelationExpressionContext(projection.expression, p.source.columns, row, projection.outer)
	}
	if projection.column < 0 || projection.column >= len(row.values) {
		return exprValue{}, sqlFailure{1105, "HY000", "row shape does not match SELECT projection"}
	}
	return relationColumnValue(p.source.columns, projection.column, row)
}

func (p *relationalSelectPlan) projectOrderValues(row relationRow, result *relationalResultRow) error {
	for index, order := range p.order {
		if !order.computed {
			continue
		}
		value, err := evaluateRelationExpressionContext(order.expression, p.source.columns, row, p.outer)
		if err != nil {
			return err
		}
		result.orders[index] = value
	}
	return nil
}

func relationExpressionMetadata(expression string, columns []relationColumn) (columnMetadata, error) {
	return relationExpressionMetadataContext(expression, columns, nil)
}

func relationExpressionMetadataContext(expression string, columns []relationColumn, outer *outerRelationScope) (columnMetadata, error) {
	value, err := representativeExpressionValueContext(expression, columns, outer)
	if err != nil {
		return columnMetadata{}, err
	}
	metadata := scalarMetadata(expression, value.render(), value)
	metadata.flags &^= mysqlNotNullFlag
	switch value.kind {
	case valueInt, valueUint:
		metadata.length = 20
	case valueDecimal:
		metadata.length = decimalPrecisionCeiling + 2
	case valueDouble:
		metadata.length = 22
	case valueString:
		metadata.length = relationStringExpressionLength(expression, columns, value.render())
	}
	return metadata, nil
}

func representativeExpressionValue(expression string, columns []relationColumn) (exprValue, error) {
	return representativeExpressionValueContext(expression, columns, nil)
}

func representativeExpressionValueContext(expression string, columns []relationColumn, outer *outerRelationScope) (exprValue, error) {
	var firstError error
	for _, sample := range []int64{1, 2, 10, 1000, 0, -1} {
		value, err := evaluateScalarWithResolver(expression, func(name string) (exprValue, error) {
			index, err := resolveRelationColumn(name, columns)
			if err != nil {
				return outerRelationValue(name, outer)
			}
			return relationMetadataValue(columns[index], sample), nil
		})
		if err == nil {
			return value, nil
		}
		if firstError == nil {
			firstError = err
		}
	}
	return exprValue{}, firstError
}

func relationMetadataValue(column relationColumn, sample int64) exprValue {
	typ, err := parseNumericType(column.typeName)
	if err != nil {
		return stringValue("x")
	}
	switch typ.kind {
	case numericInteger:
		if typ.unsigned {
			if sample < 0 {
				sample = -sample
			}
			return uintValue(uint64(sample))
		}
		return intValue(sample)
	case numericDecimal:
		value, _ := parseDecimalText(strconv.FormatInt(sample, 10) + strings.Repeat("0", typ.scale))
		value.scale = typ.scale
		return decimalValueOf(value)
	case numericFloat:
		return doubleValue(float64(sample))
	case numericBoolean, numericBit:
		return intValue(sample)
	default:
		return stringValue("x")
	}
}

func relationStringExpressionLength(expression string, columns []relationColumn, rendered string) uint32 {
	var length uint64
	seen := make(map[int]bool)
	tokens, _ := tokenizeExpression(expression)
	for _, token := range tokens {
		if token.kind != tokenIdent {
			continue
		}
		index, err := resolveRelationColumn(token.text, columns)
		if err == nil && !seen[index] {
			length += uint64(columns[index].metadata.length)
			seen[index] = true
		}
	}
	if length == 0 {
		return uint32(len([]rune(rendered)) * 4)
	}
	if length > uint64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(length)
}

func parseRelationalOrder(text string, projections []relationalProjection, columns []relationColumn) ([]relationalOrder, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	orders := make([]relationalOrder, 0, len(splitCSV(text)))
	for _, item := range splitCSV(text) {
		order, err := parseRelationalOrderItem(item, projections, columns)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, nil
}

func parseRelationalOrderItem(item string, projections []relationalProjection, columns []relationColumn) (relationalOrder, error) {
	expression, direction := splitOrderDirection(item)
	order := relationalOrder{expression: expression, direction: direction, column: -1, projection: -1}
	if ordinal, err := strconv.Atoi(expression); err == nil {
		if ordinal < 1 || ordinal > len(projections) {
			return relationalOrder{}, sqlFailure{1054, "42S22", "Unknown column '" + expression + "' in 'order clause'"}
		}
		order.projection, order.fromProjection = ordinal-1, true
		return order, nil
	}
	if projection, ok := projectionIndex(projections, expression); ok {
		order.projection, order.fromProjection = projection, true
		if !projections[projection].scalar {
			order.column = projections[projection].column
		}
		return order, nil
	}
	if column, err := resolveRelationColumn(expression, columns); err == nil {
		order.column = column
		return order, nil
	}
	if _, err := evaluateRelationExpression(expression, columns, sampleRelationRow(columns)); err != nil {
		return relationalOrder{}, sqlFailure{1054, "42S22", "Unknown column '" + expression + "' in 'order clause'"}
	}
	order.computed = true
	return order, nil
}

func splitOrderDirection(item string) (string, string) {
	fields := strings.Fields(strings.TrimSpace(item))
	if len(fields) > 1 {
		last := strings.ToLower(fields[len(fields)-1])
		if last == "asc" || last == "desc" {
			return strings.TrimSpace(strings.TrimSuffix(item, fields[len(fields)-1])), strings.ToUpper(last)
		}
	}
	return strings.TrimSpace(item), "ASC"
}

func projectionIndex(projections []relationalProjection, expression string) (int, bool) {
	for index, projection := range projections {
		if strings.EqualFold(strings.TrimSpace(projection.expression), strings.TrimSpace(expression)) {
			return index, true
		}
		if name, identifier := singleIdentifier(expression); identifier &&
			(identifiersEqual(projection.alias, name) || identifiersEqual(projection.name, name)) {
			return index, true
		}
	}
	return 0, false
}

func parseRelationalLimit(text string) (relationalLimit, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return relationalLimit{}, nil
	}
	if offset := keywordAt(text, "offset"); offset >= 0 {
		count, err := nonNegativeLimitValue(strings.TrimSpace(text[:offset]))
		if err != nil {
			return relationalLimit{}, err
		}
		start, err := nonNegativeLimitValue(strings.TrimSpace(text[offset+len("offset"):]))
		if err != nil {
			return relationalLimit{}, err
		}
		return relationalLimit{present: true, offset: start, count: count}, nil
	}
	parts := splitCSV(text)
	if len(parts) == 1 {
		count, err := nonNegativeLimitValue(parts[0])
		return relationalLimit{present: true, count: count}, err
	}
	if len(parts) != 2 {
		return relationalLimit{}, sqlFailure{1064, "42000", "malformed LIMIT clause"}
	}
	offset, err := nonNegativeLimitValue(parts[0])
	if err != nil {
		return relationalLimit{}, err
	}
	count, err := nonNegativeLimitValue(parts[1])
	return relationalLimit{present: true, offset: offset, count: count}, err
}

func nonNegativeLimitValue(text string) (int, error) {
	value, err := evaluateScalar(strings.TrimSpace(text))
	if err != nil {
		return 0, sqlFailure{1064, "42000", "invalid LIMIT value"}
	}
	var result uint64
	switch value.kind {
	case valueInt:
		if value.i < 0 {
			return 0, sqlFailure{1064, "42000", "invalid LIMIT value"}
		}
		result = uint64(value.i)
	case valueUint:
		result = value.u
	default:
		return 0, sqlFailure{1064, "42000", "invalid LIMIT value"}
	}
	if result > uint64(maxIntValue()) {
		return maxIntValue(), nil
	}
	return int(result), nil
}

func maxIntValue() int { return int(^uint(0) >> 1) }

func (p *relationalSelectPlan) shapeRows(rows []relationalResultRow) []relationalResultRow {
	rows = distinctRelationalRows(rows, p.distinct, p.projection, p.source.columns)
	rows = sortRelationalRows(rows, p.order, p.source.columns)
	return limitRelationalRows(rows, p.limit)
}

func sortRelationalRows(rows []relationalResultRow, orders []relationalOrder, columns []relationColumn) []relationalResultRow {
	if len(orders) == 0 {
		return rows
	}
	sort.SliceStable(rows, func(left, right int) bool {
		for orderIndex, order := range orders {
			comparison := compareRelationalOrder(rows[left], rows[right], orderIndex, order, columns)
			if comparison == 0 {
				continue
			}
			return orderedBefore(comparison, order.direction)
		}
		return false
	})
	return rows
}

func orderedBefore(comparison int, direction string) bool {
	if direction == "DESC" {
		return comparison > 0
	}
	return comparison < 0
}

func distinctRelationalRows(rows []relationalResultRow, distinct bool, projections []relationalProjection, columns []relationColumn) []relationalResultRow {
	if !distinct {
		return rows
	}
	seen := make(map[string]struct{}, len(rows))
	unique := rows[:0]
	for _, row := range rows {
		key := distinctRowKey(row, projections, columns)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, row)
	}
	return unique
}

func limitRelationalRows(rows []relationalResultRow, limit relationalLimit) []relationalResultRow {
	if !limit.present {
		return rows
	}
	start := limit.offset
	if start > len(rows) {
		start = len(rows)
	}
	end := start + limit.count
	if end < start || end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

func compareRelationalOrder(left, right relationalResultRow, orderIndex int, order relationalOrder, columns []relationColumn) int {
	leftValue, rightValue := orderValues(left, right, orderIndex, order, columns)
	if leftValue.isNull() || rightValue.isNull() {
		switch {
		case leftValue.isNull() && rightValue.isNull():
			return 0
		case leftValue.isNull():
			return -1
		default:
			return 1
		}
	}
	var column *relationColumn
	if order.column >= 0 && order.column < len(columns) {
		column = &columns[order.column]
	}
	comparison, err := compareRelationalOrderValues(leftValue, rightValue, column)
	if err == nil {
		return comparison
	}
	return strings.Compare(leftValue.render(), rightValue.render())
}

func compareRelationalOrderValues(left, right exprValue, column *relationColumn) (int, error) {
	if column == nil {
		return compareOperands(left, right)
	}
	return compareRelationOperands(relationOperand{isColumn: true, definition: *column}, relationOperand{}, left, right)
}

func orderValues(left, right relationalResultRow, orderIndex int, order relationalOrder, columns []relationColumn) (exprValue, exprValue) {
	if order.fromProjection {
		return left.projections[order.projection], right.projections[order.projection]
	}
	if order.computed {
		return left.orders[orderIndex], right.orders[orderIndex]
	}
	leftValue, _ := relationColumnValue(columns, order.column, left.source)
	rightValue, _ := relationColumnValue(columns, order.column, right.source)
	return leftValue, rightValue
}

func distinctRowKey(row relationalResultRow, projections []relationalProjection, columns []relationColumn) string {
	var builder strings.Builder
	for index, value := range row.values {
		if row.nulls[index] {
			builder.WriteString("N;")
			continue
		}
		key := distinctValueKey(row, index, value, projections, columns)
		builder.WriteString("V")
		builder.WriteString(strconv.Itoa(len(key)))
		builder.WriteByte(':')
		builder.WriteString(key)
		builder.WriteByte(';')
	}
	return builder.String()
}

func distinctValueKey(row relationalResultRow, index int, value string, projections []relationalProjection, columns []relationColumn) string {
	if index >= len(row.projections) || row.projections[index].kind != valueString {
		return value
	}
	projection := projections[index]
	if projection.scalar || projection.column < 0 || projection.column >= len(columns) {
		return characterComparisonKey(defaultStringType, value)
	}
	typ, err := parseCharacterType(columns[projection.column].typeName)
	if err != nil || typ.kind != characterText {
		return value
	}
	return characterComparisonKey(typ, value)
}
