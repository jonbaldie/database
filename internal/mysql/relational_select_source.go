package mysql

import (
	"sort"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

func parseRelationalSource(s *relationExecutor, text string) (relationalSource, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return relationalSource{}, sqlFailure{1064, "42000", "malformed SELECT source"}
	}
	first, remainder, err := parseRelationalTableSource(s, text)
	if err != nil {
		return relationalSource{}, err
	}
	source := relationalSource{}
	if err := source.appendTable(first, nil); err != nil {
		return relationalSource{}, err
	}
	for strings.TrimSpace(remainder) != "" {
		join, right, next, err := parseRelationalJoin(s, strings.TrimSpace(remainder), source.columns)
		if err != nil {
			return relationalSource{}, err
		}
		if err := source.appendTable(right, join.using); err != nil {
			return relationalSource{}, err
		}
		join.columns = append([]relationColumn(nil), source.columns...)
		source.joins = append(source.joins, join)
		remainder = next
	}
	return source, nil
}

func parseRelationalJoin(s *relationExecutor, text string, left []relationColumn) (relationalJoin, relationalTableSource, string, error) {
	kind, after, ok := consumeJoinStart(text)
	if !ok {
		return relationalJoin{}, relationalTableSource{}, "", sqlFailure{1064, "42000", "malformed JOIN clause"}
	}
	right, after, err := parseRelationalTableSource(s, after)
	if err != nil {
		return relationalJoin{}, relationalTableSource{}, "", err
	}
	condition, using, remainder, err := parseRelationalJoinCondition(kind, strings.TrimSpace(after), left, right.columns)
	if err != nil {
		return relationalJoin{}, relationalTableSource{}, "", err
	}
	return relationalJoin{kind: kind, right: right, condition: condition, using: using}, right, remainder, nil
}

func parseRelationalJoinCondition(kind, text string, left, right []relationColumn) (string, []string, string, error) {
	if strings.HasPrefix(strings.ToLower(text), "using ") {
		condition, names, remainder, valid := parseJoinUsing(strings.TrimSpace(text[len("using "):]), left, right)
		if !valid {
			return "", nil, "", sqlFailure{1064, "42000", "malformed JOIN USING clause"}
		}
		return condition, names, remainder, nil
	}
	if strings.HasPrefix(strings.ToLower(text), "on ") {
		condition, remainder := splitJoinCondition(strings.TrimSpace(text[len("on "):]))
		if condition == "" {
			return "", nil, "", sqlFailure{1064, "42000", "malformed JOIN ON clause"}
		}
		return condition, nil, remainder, nil
	}
	if kind != "cross" {
		return "", nil, "", sqlFailure{1064, "42000", "JOIN requires ON or USING"}
	}
	return "", nil, text, nil
}

func (source *relationalSource) appendTable(table relationalTableSource, using []string) error {
	for _, existing := range source.tables {
		if identifiersEqual(existing.alias, table.alias) {
			return sqlFailure{1066, "42000", "Not unique table/alias: '" + table.alias + "'"}
		}
	}
	rowOffset := len(source.columns)
	for _, name := range using {
		leftIndex, leftOK := findRelationColumnIndex(source.columns, "", name)
		rightIndex, rightOK := findRelationColumnIndex(table.columns, "", name)
		if !leftOK || !rightOK {
			return sqlFailure{1054, "42S22", "unknown column '" + name + "' in 'USING clause'"}
		}
		source.columns[leftIndex].coalesce = rowOffset + rightIndex
		table.columns[rightIndex].hidden = true
	}
	source.columns = append(source.columns, table.columns...)
	source.tables = append(source.tables, table)
	return nil
}

func parseRelationalTableSource(s *relationExecutor, text string) (relationalTableSource, string, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "(") {
		return parseDerivedTableSource(s, text)
	}
	token, remainder, ok := relationToken(text)
	if !ok {
		return relationalTableSource{}, "", sqlFailure{1064, "42000", "invalid table name"}
	}
	parts, valid := splitQualifiedIdentifier(token)
	if !valid || len(parts) == 0 || len(parts) > 2 {
		return relationalTableSource{}, "", sqlFailure{1064, "42000", "invalid table name"}
	}
	if source, tail, found, err := parseCTETableSource(s, parts, remainder); found {
		return source, tail, err
	}
	return parseCatalogTableSource(s, parts, remainder)
}

func parseCTETableSource(s *relationExecutor, parts []string, remainder string) (relationalTableSource, string, bool, error) {
	if len(parts) != 1 || s.composed == nil {
		return relationalTableSource{}, "", false, nil
	}
	key := catalog.Key(parts[0])
	relation, found := s.composed.ctes[key]
	if !found {
		return relationalTableSource{}, "", false, nil
	}
	relation, err := materializeCTE(s.composed, key, relation)
	if err != nil {
		return relationalTableSource{}, "", true, err
	}
	alias, tail, err := relationAlias(remainder, relation.name)
	if err != nil {
		return relationalTableSource{}, "", true, err
	}
	table, err := queryResultTable(relation.name, relation.result)
	if err != nil {
		return relationalTableSource{}, "", true, err
	}
	columns := relationalResultColumns(relation.name, alias, table, relation.result)
	reason := relation.reason
	if relation.references > 0 {
		reason = "reuse"
	}
	relation.references++
	s.composed.ctes[key] = relation
	source := relationalTableSource{name: relation.name, alias: alias, table: table, columns: columns, query: relation.query, reason: reason}
	return source, tail, true, nil
}

func parseCatalogTableSource(s *relationExecutor, parts []string, remainder string) (relationalTableSource, string, error) {
	namespace, name, err := tableTarget(s, parts)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	table, err := relationTable(s, namespace, name)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	alias, remainder, err := relationAlias(remainder, name)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	hints, remainder, err := parseRelationalIndexHints(table, remainder)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	columns := relationalTableColumns(namespace, name, alias, table)
	return relationalTableSource{namespace: namespace, name: name, alias: alias, table: table, columns: columns, hints: hints}, remainder, nil
}

func parseDerivedTableSource(s *relationExecutor, text string) (relationalTableSource, string, error) {
	text = strings.TrimSpace(text)
	close, ok := matchingParenthesis(text, 0)
	if !ok {
		return relationalTableSource{}, "", sqlFailure{1064, "42000", "unterminated derived table"}
	}
	query := strings.TrimSpace(text[1:close])
	if query == "" {
		return relationalTableSource{}, "", sqlFailure{1064, "42000", "empty derived table"}
	}
	alias, remainder, err := requiredDerivedAlias(text[close+1:])
	if err != nil {
		return relationalTableSource{}, "", err
	}
	if s.composed == nil {
		return relationalTableSource{}, "", sqlFailure{1064, "42000", "derived table context is unavailable"}
	}
	child, err := s.composed.child()
	if err != nil {
		return relationalTableSource{}, "", err
	}
	result, err := composedSourceResult(s.composed, child, query)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	table, err := queryResultTable(alias, result)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	columns := relationalResultColumns(alias, alias, table, result)
	return relationalTableSource{name: alias, alias: alias, table: table, columns: columns, query: query, reason: "derived_table"}, remainder, nil
}

func composedSourceResult(context, child *composedQueryContext, query string) (*queryResult, error) {
	if context.planning {
		return describeComposedSelect(child, query, nil)
	}
	return executeComposedSelect(child, query, nil)
}

func relationalResultColumns(tableName, alias string, table catalog.Table, result *queryResult) []relationColumn {
	columns := relationalTableColumns("", tableName, alias, table)
	for index := range columns {
		metadata := resultColumnDefinition(columns[index].name, index, result.metadata)
		metadata.schema, metadata.table = "", alias
		columns[index].metadata = metadata
	}
	return columns
}

func requiredDerivedAlias(text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(strings.ToLower(text), "as ") {
		text = strings.TrimSpace(text[len("AS "):])
	}
	aliasToken, remainder, ok := relationToken(text)
	if !ok || isJoinWord(aliasToken) {
		return "", "", sqlFailure{1248, "42000", "every derived table must have its own alias"}
	}
	alias, valid := singleIdentifier(aliasToken)
	if !valid {
		return "", "", sqlFailure{1064, "42000", "invalid derived table alias"}
	}
	if err := validateIdentifierLength(alias); err != nil {
		return "", "", err
	}
	return alias, remainder, nil
}

func relationToken(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || text[0] == ',' {
		return "", "", false
	}
	quoted := false
	length := len(text)
	for index := 0; index < length; index++ {
		if next, handled := relationTokenBacktick(text, index, length, quoted); handled {
			quoted, index = next.quoted, next.index
			continue
		}
		if !quoted && (isRelationSpace(text[index]) || text[index] == ',') {
			return text[:index], text[index:], true
		}
	}
	return text, "", !quoted
}

type relationTokenCursor struct {
	quoted bool
	index  int
}

func relationTokenBacktick(text string, index, length int, quoted bool) (relationTokenCursor, bool) {
	if text[index] != '`' {
		return relationTokenCursor{}, false
	}
	if quoted && index+1 < length && text[index+1] == '`' {
		return relationTokenCursor{quoted: quoted, index: index + 1}, true
	}
	return relationTokenCursor{quoted: !quoted, index: index}, true
}

func relationAlias(text, tableName string) (string, string, error) {
	text = strings.TrimSpace(text)
	if text == "" || text[0] == ',' || isJoinWord(text) || isIndexHintStart(text) {
		return tableName, text, nil
	}
	if strings.HasPrefix(strings.ToLower(text), "as ") {
		return explicitRelationAlias(text[len("as "):])
	}
	return implicitRelationAlias(text, tableName)
}

func explicitRelationAlias(text string) (string, string, error) {
	alias, remainder, ok := relationToken(text)
	if !ok {
		return "", "", sqlFailure{1064, "42000", "malformed table alias"}
	}
	name, valid := singleIdentifier(alias)
	if !valid {
		return "", "", sqlFailure{1064, "42000", "invalid table alias"}
	}
	if err := validateIdentifierLength(name); err != nil {
		return "", "", err
	}
	return name, remainder, nil
}

func implicitRelationAlias(text, tableName string) (string, string, error) {
	alias, remainder, ok := relationToken(text)
	if !ok || isJoinWord(alias) || isIndexHintStart(alias) {
		return tableName, text, nil
	}
	name, valid := singleIdentifier(alias)
	if !valid {
		return tableName, text, nil
	}
	if err := validateIdentifierLength(name); err != nil {
		return "", "", err
	}
	return name, remainder, nil
}

func isIndexHintStart(text string) bool {
	fields := strings.Fields(strings.ToLower(text))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "use", "force", "ignore":
		return true
	default:
		return false
	}
}

func parseRelationalIndexHints(table catalog.Table, text string) ([]relationalIndexHint, string, error) {
	hints := []relationalIndexHint{}
	for isIndexHintStart(text) {
		hint, remainder, err := parseRelationalIndexHint(table, text)
		if err != nil {
			return nil, "", err
		}
		hints = append(hints, hint)
		text = remainder
	}
	if err := validateRelationalIndexHints(hints); err != nil {
		return nil, "", err
	}
	return hints, text, nil
}

func parseRelationalIndexHint(table catalog.Table, text string) (relationalIndexHint, string, error) {
	kind, remainder, err := parseIndexHintStart(text)
	if err != nil {
		return relationalIndexHint{}, "", err
	}
	remainder, err = consumeIndexHintKeyword(remainder)
	if err != nil {
		return relationalIndexHint{}, "", err
	}
	scope, remainder, err := parseIndexHintScope(remainder)
	if err != nil {
		return relationalIndexHint{}, "", err
	}
	names, remainder, err := parseIndexHintNames(table, remainder)
	if err != nil {
		return relationalIndexHint{}, "", err
	}
	if !strings.EqualFold(kind, "use") && len(names) == 0 {
		return relationalIndexHint{}, "", sqlFailure{1064, "42000", "index hint requires an index name"}
	}
	return relationalIndexHint{kind: strings.ToLower(kind), scope: scope, indexes: names}, remainder, nil
}

func parseIndexHintStart(text string) (string, string, error) {
	kind, remainder, valid := relationToken(text)
	if !valid || !isIndexHintStart(kind) {
		return "", "", sqlFailure{1064, "42000", "invalid index hint"}
	}
	return kind, strings.TrimSpace(remainder), nil
}

func consumeIndexHintKeyword(value string) (string, error) {
	keyword, remainder, valid := consumeIdentifier(value)
	if !valid || !isIndexHintKeyword(keyword) {
		return "", sqlFailure{1064, "42000", "index hint requires INDEX or KEY"}
	}
	return remainder, nil
}

func isIndexHintKeyword(value string) bool {
	return strings.EqualFold(value, "index") || strings.EqualFold(value, "key")
}

func parseIndexHintScope(text string) (string, string, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(text), "for ") {
		return "join", text, nil
	}
	scope, remainder, valid := consumeIdentifier(strings.TrimSpace(text[len("FOR "):]))
	if !valid {
		return "", "", sqlFailure{1064, "42000", "invalid index hint scope"}
	}
	return indexHintScope(strings.ToLower(scope), remainder)
}

func indexHintScope(scope, remainder string) (string, string, error) {
	switch strings.ToLower(scope) {
	case "join":
		return "join", remainder, nil
	case "order":
		return indexHintByScope("order", remainder)
	case "group":
		return indexHintByScope("group", remainder)
	default:
		return "", "", sqlFailure{1064, "42000", "invalid index hint scope"}
	}
}

func indexHintByScope(scope, remainder string) (string, string, error) {
	by, after, valid := consumeIdentifier(strings.TrimSpace(remainder))
	if !valid || !strings.EqualFold(by, "by") {
		return "", "", sqlFailure{1064, "42000", "index hint " + strings.ToUpper(scope) + " requires BY"}
	}
	return scope + "_by", after, nil
}

func parseIndexHintNames(table catalog.Table, text string) ([]string, string, error) {
	body, remainder, valid := consumeParenthesized(strings.TrimSpace(text))
	if !valid {
		return nil, "", sqlFailure{1064, "42000", "index hint requires a key list"}
	}
	if strings.TrimSpace(body) == "" {
		return []string{}, remainder, nil
	}
	names := splitCSV(body)
	resolved := make([]string, len(names))
	for number, name := range names {
		identifier, valid := singleIdentifier(strings.TrimSpace(name))
		if !valid {
			return nil, "", sqlFailure{1064, "42000", "invalid index name in hint"}
		}
		match, err := resolveHintIndex(table, identifier)
		if err != nil {
			return nil, "", err
		}
		resolved[number] = match
	}
	return resolved, remainder, nil
}

func resolveHintIndex(table catalog.Table, name string) (string, error) {
	matches := []catalog.Index{}
	for _, index := range effectiveTableIndexes(table) {
		if strings.HasPrefix(catalog.Key(index.Name), catalog.Key(name)) {
			matches = append(matches, index)
		}
	}
	if len(matches) != 1 {
		return "", sqlFailure{1176, "42000", "key '" + name + "' doesn't exist in table '" + table.Name + "'"}
	}
	if matches[0].Invisible {
		return "", sqlFailure{3522, "HY000", "an invisible index cannot be used in a hint"}
	}
	return matches[0].Name, nil
}

func validateRelationalIndexHints(hints []relationalIndexHint) error {
	seenUse, seenForce := map[string]bool{}, map[string]bool{}
	for _, hint := range hints {
		if hint.kind == "use" {
			seenUse[hint.scope] = true
		}
		if hint.kind == "force" {
			seenForce[hint.scope] = true
		}
	}
	for scope := range seenUse {
		if seenForce[scope] {
			return sqlFailure{1064, "42000", "USE INDEX and FORCE INDEX cannot be used together"}
		}
	}
	return nil
}

func isJoinWord(text string) bool {
	word := strings.ToLower(strings.Fields(text)[0])
	switch word {
	case "join", "inner", "left", "right", "cross", "outer", "on", "using":
		return true
	default:
		return false
	}
}

func consumeJoinStart(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, ",") {
		return "cross", strings.TrimSpace(text[1:]), true
	}
	for _, candidate := range []struct {
		prefix string
		kind   string
	}{
		{"left outer join ", "left"},
		{"right outer join ", "right"},
		{"inner join ", "inner"},
		{"cross join ", "cross"},
		{"left join ", "left"},
		{"right join ", "right"},
		{"join ", "inner"},
	} {
		if strings.HasPrefix(strings.ToLower(text), candidate.prefix) {
			return candidate.kind, strings.TrimSpace(text[len(candidate.prefix):]), true
		}
	}
	return "", "", false
}

func parseJoinUsing(text string, left, right []relationColumn) (string, []string, string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(text), "(") {
		return "", nil, "", false
	}
	close, ok := matchingParenthesis(text, 0)
	if !ok {
		return "", nil, "", false
	}
	items := splitCSV(text[1:close])
	if len(items) == 0 {
		return "", nil, "", false
	}
	condition, names, valid := joinUsingConditions(items, left, right)
	return condition, names, strings.TrimSpace(text[close+1:]), valid
}

func joinUsingConditions(items []string, left, right []relationColumn) (string, []string, bool) {
	conditions := make([]string, 0, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		condition, name, valid := joinUsingCondition(item, left, right)
		if !valid {
			return "", nil, false
		}
		conditions = append(conditions, condition)
		names = append(names, name)
	}
	return strings.Join(conditions, " AND "), names, true
}

func joinUsingCondition(item string, left, right []relationColumn) (string, string, bool) {
	name, valid := singleIdentifier(item)
	if !valid {
		return "", "", false
	}
	leftColumn, leftOK := findNamedColumn(left, "", name)
	rightColumn, rightOK := findNamedColumn(right, "", name)
	if !leftOK || !rightOK {
		return "", "", false
	}
	leftReference := quoteIdentifier(leftColumn.qualifier) + "." + quoteIdentifier(leftColumn.name)
	rightReference := quoteIdentifier(rightColumn.qualifier) + "." + quoteIdentifier(rightColumn.name)
	return leftReference + " = " + rightReference, name, true
}

func splitJoinCondition(text string) (string, string) {
	positions := make([]int, 0, 4)
	for _, word := range []string{"join", "where", "order", "limit"} {
		if position := keywordAt(text, word); position >= 0 {
			positions = append(positions, position)
		}
	}
	if len(positions) == 0 {
		return strings.TrimSpace(text), ""
	}
	sort.Ints(positions)
	position := positions[0]
	return strings.TrimSpace(text[:position]), strings.TrimSpace(text[position:])
}

func relationalTableColumns(namespace, tableName, alias string, table catalog.Table) []relationColumn {
	columns := make([]relationColumn, len(table.Columns))
	for index, name := range table.Columns {
		metadata := tableMetadata(namespace, tableName, table, []int{index})[0]
		typeName, _ := table.ColumnType(index)
		columns[index] = relationColumn{
			namespace: namespace, table: tableName, qualifier: alias, name: name,
			typeName: typeName, index: index, coalesce: -1, metadata: metadata, tableDefinition: table,
		}
	}
	return columns
}

func findRelationColumnIndex(columns []relationColumn, qualifier, name string) (int, bool) {
	found := -1
	for index, column := range columns {
		if column.hidden && qualifier == "" {
			continue
		}
		if !relationColumnMatchesName(column, qualifier, name) {
			continue
		}
		if found >= 0 {
			return -1, false
		}
		found = index
	}
	return found, found >= 0
}

type relationRowYield func(relationRow) error
type relationRowIterator func(relationRowYield) error

func (p *relationalSelectPlan) forEachSourceRow(yield relationRowYield) error {
	return p.source.rowIterator()(yield)
}

func (source relationalSource) rowIterator() relationRowIterator {
	iterator := tableRowIterator(source.tables[0])
	leftWidth := len(source.tables[0].columns)
	leftTables := 1
	for _, join := range source.joins {
		iterator = joinedRowIterator(iterator, join, leftWidth, leftTables)
		leftWidth += len(join.right.columns)
		leftTables++
	}
	return iterator
}

func tableRowIterator(table relationalTableSource) relationRowIterator {
	return func(yield relationRowYield) error {
		rows, err := indexScanRows(table)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := yield(relationRow{values: append([]string(nil), row.values...), lockKeys: []string{rowLockKey(row.values)}}); err != nil {
				return err
			}
		}
		return nil
	}
}

type indexedRelationRow struct {
	values []string
	keys   []string
}

func indexScanRows(table relationalTableSource) ([]indexedRelationRow, error) {
	if table.access == nil {
		rows := make([]indexedRelationRow, len(table.table.Rows))
		for index, values := range table.table.Rows {
			rows[index] = indexedRelationRow{values: values}
		}
		return rows, nil
	}
	rows := make([]indexedRelationRow, len(table.table.Rows))
	for number, values := range table.table.Rows {
		keys, err := indexScanKeys(table, *table.access, values)
		if err != nil {
			return nil, err
		}
		rows[number] = indexedRelationRow{values: values, keys: keys}
	}
	sort.SliceStable(rows, func(left, right int) bool {
		return orderedIndexRowBefore(rows[left], rows[right], *table.access)
	})
	return rows, nil
}

func indexScanKeys(table relationalTableSource, index catalog.Index, row []string) ([]string, error) {
	keys := make([]string, len(index.Parts))
	for number, part := range index.Parts {
		key, err := indexScanPartKey(table, part, row)
		if err != nil {
			return nil, err
		}
		keys[number] = key
	}
	return keys, nil
}

func indexScanPartKey(table relationalTableSource, part catalog.IndexPart, row []string) (string, error) {
	if part.Expression != "" {
		value, err := evaluateRelationExpression(part.Expression, table.columns, relationRow{values: row})
		if err != nil || value.isNull() {
			return "", err
		}
		return indexPrefixKey(value.render(), part.PrefixLength), nil
	}
	column := tableColumnIndex(table.table.Columns, part.Column)
	if column < 0 || row[column] == storedSQLNullValue {
		return "", nil
	}
	return indexPrefixKey(constraintColumnKey(table.table, column, row[column]), part.PrefixLength), nil
}

func orderedIndexRowBefore(left, right indexedRelationRow, index catalog.Index) bool {
	for number, key := range left.keys {
		if key == right.keys[number] {
			continue
		}
		if index.Parts[number].Descending {
			return key > right.keys[number]
		}
		return key < right.keys[number]
	}
	return false
}

func joinedRowIterator(left relationRowIterator, join relationalJoin, leftWidth, leftTables int) relationRowIterator {
	return func(yield relationRowYield) error {
		matchedRight := make([]bool, len(join.right.table.Rows))
		if err := left(func(row relationRow) error {
			return yieldJoinedRows(row, join, matchedRight, yield)
		}); err != nil {
			return err
		}
		if join.kind != "right" {
			return nil
		}
		return yieldUnmatchedRight(join.right.table.Rows, matchedRight, leftWidth, leftTables, yield)
	}
}

func yieldJoinedRows(left relationRow, join relationalJoin, matchedRight []bool, yield relationRowYield) error {
	matched := false
	for rightIndex, values := range join.right.table.Rows {
		candidate := relationRow{values: append(append([]string(nil), left.values...), values...), lockKeys: append(append([]string(nil), left.lockKeys...), rowLockKey(values))}
		ok, err := joinCandidateMatches(join, candidate)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		matched, matchedRight[rightIndex] = true, true
		if err := yield(candidate); err != nil {
			return err
		}
	}
	if !matched && join.kind == "left" {
		return yield(appendNullRight(left, len(join.right.columns)))
	}
	return nil
}

func yieldUnmatchedRight(rows [][]string, matched []bool, leftWidth, leftTables int, yield relationRowYield) error {
	for index, values := range rows {
		if matched[index] {
			continue
		}
		if err := yield(appendNullLeft(values, leftWidth, leftTables)); err != nil {
			return err
		}
	}
	return nil
}

func appendNullLeft(right []string, width, leftTables int) relationRow {
	values := make([]string, width, width+len(right))
	for index := range values {
		values[index] = storedSQLNullValue
	}
	values = append(values, right...)
	lockKeys := make([]string, leftTables, leftTables+1)
	lockKeys = append(lockKeys, rowLockKey(right))
	return relationRow{values: values, lockKeys: lockKeys}
}

func joinCandidateMatches(join relationalJoin, row relationRow) (bool, error) {
	if join.predicate == nil {
		return true, nil
	}
	return predicateMatches(join.predicate, row)
}

func appendNullRight(left relationRow, width int) relationRow {
	values := append([]string(nil), left.values...)
	for index := 0; index < width; index++ {
		values = append(values, storedSQLNullValue)
	}
	lockKeys := append(append([]string(nil), left.lockKeys...), "")
	return relationRow{values: values, lockKeys: lockKeys}
}
