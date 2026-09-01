package mysql

import (
	"sort"
	"strconv"
	"strings"
	"time"

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
	if source, tail, found, err := parseInformationSchemaTableSource(s, parts, remainder); found {
		return source, tail, err
	}
	return parseCatalogTableSource(s, parts, remainder)
}

func parseInformationSchemaTableSource(s *relationExecutor, parts []string, remainder string) (relationalTableSource, string, bool, error) {
	name, ok := informationSchemaTableName(s, parts)
	if !ok {
		return relationalTableSource{}, "", false, nil
	}
	view, found := findInformationSchemaView(name)
	if !found {
		return relationalTableSource{}, "", true, sqlFailure{1105, "HY000", "unsupported information_schema view '" + name + "'"}
	}
	result := informationSchemaQueryResult(s.session, view)
	table, err := queryResultTable(view.name, result)
	if err != nil {
		return relationalTableSource{}, "", true, err
	}
	alias, tail, err := relationAlias(remainder, view.name)
	if err != nil {
		return relationalTableSource{}, "", true, err
	}
	columns := relationalResultColumns(view.name, alias, table, result)
	return relationalTableSource{namespace: informationSchemaName, name: view.name, alias: alias, table: table, columns: columns}, tail, true, nil
}

func informationSchemaTableName(s *relationExecutor, parts []string) (string, bool) {
	if len(parts) == 2 && strings.EqualFold(parts[0], informationSchemaName) {
		return parts[1], true
	}
	if len(parts) == 1 && s != nil && strings.EqualFold(s.database, informationSchemaName) {
		return parts[0], true
	}
	return "", false
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
	reference := relation.references
	reason := relation.reason
	if reference > 0 {
		reason = "reuse"
	}
	relation.references++
	s.composed.ctes[key] = relation
	materializeKey := relation.materializeKey
	if reference > 0 {
		materializeKey += "/reuse/" + strconv.Itoa(reference)
	}
	source := relationalTableSource{name: relation.name, alias: alias, table: table, columns: columns, query: relation.query, reason: reason, materializeKey: materializeKey, runtimePrefix: relation.runtimePrefix}
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
	child, runtimePrefix, err := s.composed.inputChild()
	if err != nil {
		return relationalTableSource{}, "", err
	}
	started := time.Now()
	result, err := composedSourceResult(s.composed, child, query)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	table, err := queryResultTable(alias, result)
	if err != nil {
		return relationalTableSource{}, "", err
	}
	columns := relationalResultColumns(alias, alias, table, result)
	materializeKey := composedMaterializeKey(child, alias, query, 0)
	recordMaterializedResult(s.composed, materializeKey, result, time.Since(started))
	return relationalTableSource{name: alias, alias: alias, table: table, columns: columns, query: query, reason: "derived_table", materializeKey: materializeKey, runtimePrefix: runtimePrefix}, remainder, nil
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
	for _, word := range []string{"left outer join", "right outer join", "inner join", "cross join", "left join", "right join", "join", "where", "order", "limit"} {
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
	return p.source.rowIterator()(func(row relationRow) error {
		if err := p.session.checkStatementResources(); err != nil {
			return err
		}
		return yield(row)
	})
}

func (source relationalSource) rowIterator() relationRowIterator {
	iterator := tableRowIterator(source.tables[0], source.runtime, 0)
	leftWidth := len(source.tables[0].columns)
	leftTables := 1
	for _, join := range source.joins {
		iterator = joinedRowIterator(iterator, join, leftWidth, leftTables, source.runtime)
		leftWidth += len(join.right.columns)
		leftTables++
	}
	return iterator
}

func tableRowIterator(table relationalTableSource, runtime *selectRuntimeBinding, scanIndex int) relationRowIterator {
	return func(yield relationRowYield) error {
		started := time.Now()
		rows, err := indexScanRows(table)
		if err != nil {
			return err
		}
		elapsed := time.Since(started)
		bytes := sourceRowBytes(table.table.Rows)
		runtime.recordSourceScan(scanIndex, table, len(rows), bytes, elapsed)
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
}

type relationalIndexBound struct {
	value     exprValue
	present   bool
	inclusive bool
}

type relationalIndexBounds struct {
	lower relationalIndexBound
	upper relationalIndexBound
}

func indexScanRows(table relationalTableSource) ([]indexedRelationRow, error) {
	if table.access == nil {
		rows := make([]indexedRelationRow, len(table.table.Rows))
		for index, values := range table.table.Rows {
			rows[index] = indexedRelationRow{values: values}
		}
		return rows, nil
	}
	entries, err := table.table.CachedOrderedIndex(indexScanCacheKey(table.table, *table.access), func() ([]catalog.OrderedIndexRow, error) {
		return buildOrderedIndex(table, *table.access)
	})
	if err != nil {
		return nil, err
	}
	entries, err = boundedOrderedIndex(entries, table, *table.access)
	if err != nil {
		return nil, err
	}
	rows := make([]indexedRelationRow, len(entries))
	for number, entry := range entries {
		rows[number] = indexedRelationRow{values: table.table.Rows[entry.Position]}
	}
	return rows, nil
}

func relationIndexBounds(table relationalTableSource, index catalog.Index, where string, session *session) (*relationalIndexBounds, error) {
	columnPosition, column, eligible := relationIndexBoundColumn(table, index, where)
	if !eligible {
		return nil, nil
	}
	text := stripRelationParentheses(strings.TrimSpace(where))
	if len(splitRelationKeyword(text, "OR")) > 1 {
		return nil, nil
	}
	bounds := &relationalIndexBounds{}
	for _, part := range splitRelationKeyword(text, "AND") {
		if err := applyRelationIndexPredicate(bounds, part, table.columns, columnPosition, column, session); err != nil {
			return nil, err
		}
	}
	if !bounds.lower.present && !bounds.upper.present {
		return nil, nil
	}
	return bounds, nil
}

func relationIndexBoundColumn(table relationalTableSource, index catalog.Index, where string) (int, relationColumn, bool) {
	if strings.TrimSpace(where) == "" || len(index.Parts) == 0 {
		return 0, relationColumn{}, false
	}
	part := index.Parts[0]
	if part.Column == "" || part.PrefixLength != 0 {
		return 0, relationColumn{}, false
	}
	position := tableColumnIndex(table.table.Columns, part.Column)
	if position < 0 || position >= len(table.columns) {
		return 0, relationColumn{}, false
	}
	return position, table.columns[position], true
}

func applyRelationIndexPredicate(bounds *relationalIndexBounds, predicate string, columns []relationColumn, position int, column relationColumn, session *session) error {
	operator, left, right, found := findRelationComparison(stripRelationParentheses(strings.TrimSpace(predicate)))
	if !found {
		return nil
	}
	value, normalized, found, err := relationIndexBoundValue(left, right, operator, columns, position, column, session)
	if err != nil || !found || value.isNull() {
		return err
	}
	applyRelationIndexBound(bounds, normalized, value, column)
	return nil
}

func relationIndexBoundValue(left, right, operator string, columns []relationColumn, target int, column relationColumn, session *session) (exprValue, string, bool, error) {
	leftIsTarget := relationIndexTargetColumn(left, columns, target)
	rightIsTarget := relationIndexTargetColumn(right, columns, target)
	if leftIsTarget == rightIsTarget {
		return exprValue{}, "", false, nil
	}
	literalText := right
	if rightIsTarget {
		literalText = left
		operator = reversedRelationIndexOperator(operator)
	}
	literal, err := compileRelationOperand(literalText, columns, session)
	if err != nil {
		// A valid predicate can reference a column from an outer query scope.
		// That value is not available to this local index planner, so leave the
		// predicate unbounded and let the compiled predicate evaluate it at row
		// time. Predicate compilation has already validated the expression.
		return exprValue{}, "", false, nil
	}
	if literal.isColumn || literal.computed || literal.bound {
		return exprValue{}, "", false, nil
	}
	literal, err = bindLiteralToColumn(literal, column, session)
	if err != nil {
		return exprValue{}, "", false, err
	}
	switch operator {
	case "=", "<", "<=", ">", ">=":
		return literal.value, operator, true, nil
	default:
		return exprValue{}, "", false, nil
	}
}

func relationIndexTargetColumn(text string, columns []relationColumn, target int) bool {
	position, err := resolveRelationColumn(strings.TrimSpace(text), columns)
	if err != nil {
		return false
	}
	return position == target
}

func reversedRelationIndexOperator(operator string) string {
	switch operator {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return operator
	}
}

func applyRelationIndexBound(bounds *relationalIndexBounds, operator string, value exprValue, column relationColumn) {
	switch operator {
	case "=":
		setRelationIndexLowerBound(&bounds.lower, value, true, column)
		setRelationIndexUpperBound(&bounds.upper, value, true, column)
	case ">":
		setRelationIndexLowerBound(&bounds.lower, value, false, column)
	case ">=":
		setRelationIndexLowerBound(&bounds.lower, value, true, column)
	case "<":
		setRelationIndexUpperBound(&bounds.upper, value, false, column)
	case "<=":
		setRelationIndexUpperBound(&bounds.upper, value, true, column)
	}
}

func setRelationIndexLowerBound(bound *relationalIndexBound, value exprValue, inclusive bool, column relationColumn) {
	comparison := 1
	if bound.present {
		comparison = compareOrderedIndexValues(value, bound.value, &column)
	}
	if !bound.present || comparison > 0 || comparison == 0 && !inclusive {
		*bound = relationalIndexBound{value: value, present: true, inclusive: inclusive}
	}
}

func setRelationIndexUpperBound(bound *relationalIndexBound, value exprValue, inclusive bool, column relationColumn) {
	comparison := -1
	if bound.present {
		comparison = compareOrderedIndexValues(value, bound.value, &column)
	}
	if !bound.present || comparison < 0 || comparison == 0 && !inclusive {
		*bound = relationalIndexBound{value: value, present: true, inclusive: inclusive}
	}
}

func boundedOrderedIndex(entries []catalog.OrderedIndexRow, table relationalTableSource, index catalog.Index) ([]catalog.OrderedIndexRow, error) {
	if table.bounds == nil || len(index.Parts) == 0 {
		return entries, nil
	}
	column := orderedIndexColumn(table, index.Parts[0])
	if column == nil {
		return entries, nil
	}
	valueAt := orderedIndexValueReader(entries, column)
	start, end := ascendingOrderedIndexBounds(len(entries), table.bounds, column, valueAt)
	if index.Parts[0].Descending {
		start, end = descendingOrderedIndexBounds(len(entries), table.bounds, column, valueAt)
	}
	if start > end {
		start = end
	}
	return entries[start:end], nil
}

func orderedIndexValueReader(entries []catalog.OrderedIndexRow, column *relationColumn) func(int) exprValue {
	return func(position int) exprValue {
		entry := entries[position]
		if entry.Nulls[0] {
			return nullValue()
		}
		value, _ := relationStoredValue(*column, entry.Keys[0])
		return value
	}
}

func ascendingOrderedIndexBounds(length int, bounds *relationalIndexBounds, column *relationColumn, valueAt func(int) exprValue) (int, int) {
	start, end := 0, length
	if bounds.lower.present {
		start = sort.Search(length, func(position int) bool {
			comparison := compareOrderedIndexValues(valueAt(position), bounds.lower.value, column)
			return comparison > 0 || comparison == 0 && bounds.lower.inclusive
		})
	}
	if bounds.upper.present {
		end = sort.Search(length, func(position int) bool {
			comparison := compareOrderedIndexValues(valueAt(position), bounds.upper.value, column)
			return comparison > 0 || comparison == 0 && !bounds.upper.inclusive
		})
	}
	return start, end
}

func descendingOrderedIndexBounds(length int, bounds *relationalIndexBounds, column *relationColumn, valueAt func(int) exprValue) (int, int) {
	start, end := 0, length
	if bounds.upper.present {
		start = sort.Search(length, func(position int) bool {
			comparison := compareOrderedIndexValues(valueAt(position), bounds.upper.value, column)
			return comparison < 0 || comparison == 0 && bounds.upper.inclusive
		})
	}
	if bounds.lower.present {
		end = sort.Search(length, func(position int) bool {
			comparison := compareOrderedIndexValues(valueAt(position), bounds.lower.value, column)
			return comparison < 0 || comparison == 0 && !bounds.lower.inclusive
		})
	}
	return start, end
}

type orderedIndexBuildRow struct {
	entry  catalog.OrderedIndexRow
	values []exprValue
}

func buildOrderedIndex(table relationalTableSource, index catalog.Index) ([]catalog.OrderedIndexRow, error) {
	rows := make([]orderedIndexBuildRow, len(table.table.Rows))
	for position, row := range table.table.Rows {
		entry := catalog.OrderedIndexRow{Position: position, Keys: make([]string, len(index.Parts)), Nulls: make([]bool, len(index.Parts))}
		values := make([]exprValue, len(index.Parts))
		for number, part := range index.Parts {
			key, value, err := indexScanPartValue(table, part, row)
			if err != nil {
				return nil, err
			}
			entry.Keys[number], entry.Nulls[number], values[number] = key, value.isNull(), value
		}
		rows[position] = orderedIndexBuildRow{entry: entry, values: values}
	}
	sort.SliceStable(rows, func(left, right int) bool {
		return orderedIndexBuildRowBefore(rows[left], rows[right], table, index)
	})
	entries := make([]catalog.OrderedIndexRow, len(rows))
	for position, row := range rows {
		entries[position] = row.entry
	}
	return entries, nil
}

func indexScanPartValue(table relationalTableSource, part catalog.IndexPart, row []string) (string, exprValue, error) {
	if part.Expression != "" {
		value, err := evaluateRelationExpression(part.Expression, table.columns, relationRow{values: row})
		if err != nil {
			return "", exprValue{}, err
		}
		return indexedValueWithPrefix(value, part.PrefixLength)
	}
	column := tableColumnIndex(table.table.Columns, part.Column)
	if column < 0 || row[column] == storedSQLNullValue {
		return "", nullValue(), nil
	}
	value, err := relationStoredValue(table.columns[column], row[column])
	if err != nil {
		return "", exprValue{}, err
	}
	return indexedValueWithPrefix(value, part.PrefixLength)
}

func indexedValueWithPrefix(value exprValue, prefixLength int) (string, exprValue, error) {
	if value.isNull() {
		return "", value, nil
	}
	key := indexPrefixKey(value.render(), prefixLength)
	if prefixLength > 0 {
		value = stringValue(key)
	}
	return key, value, nil
}

func orderedIndexBuildRowBefore(left, right orderedIndexBuildRow, table relationalTableSource, index catalog.Index) bool {
	for number := range index.Parts {
		comparison := compareOrderedIndexValues(left.values[number], right.values[number], orderedIndexColumn(table, index.Parts[number]))
		if comparison == 0 {
			continue
		}
		if index.Parts[number].Descending {
			return comparison > 0
		}
		return comparison < 0
	}
	return false
}

func compareOrderedIndexValues(left, right exprValue, column *relationColumn) int {
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
	comparison, err := compareRelationalOrderValues(left, right, column)
	if err == nil {
		return comparison
	}
	return strings.Compare(left.render(), right.render())
}

func orderedIndexColumn(table relationalTableSource, part catalog.IndexPart) *relationColumn {
	if part.Column == "" {
		return nil
	}
	position := tableColumnIndex(table.table.Columns, part.Column)
	if position < 0 || position >= len(table.columns) {
		return nil
	}
	return &table.columns[position]
}

func indexScanCacheKey(table catalog.Table, index catalog.Index) string {
	var key strings.Builder
	writeIndexCacheKeyField(&key, index.Name)
	for _, typeName := range table.ColumnTypes {
		writeIndexCacheKeyField(&key, typeName)
	}
	for _, part := range index.Parts {
		writeIndexCacheKeyField(&key, part.Column)
		writeIndexCacheKeyField(&key, part.Expression)
		writeIndexCacheKeyField(&key, strconv.Itoa(part.PrefixLength))
		if part.Descending {
			key.WriteByte('D')
		} else {
			key.WriteByte('A')
		}
	}
	return key.String()
}

func writeIndexCacheKeyField(key *strings.Builder, value string) {
	key.WriteString(strconv.Itoa(len(value)))
	key.WriteByte(':')
	key.WriteString(value)
}

func joinedRowIterator(left relationRowIterator, join relationalJoin, leftWidth, leftTables int, runtime *selectRuntimeBinding) relationRowIterator {
	if join.hash != nil {
		return hashJoinedRowIterator(left, join, leftWidth, leftTables, runtime)
	}
	return func(yield relationRowYield) error {
		started := time.Now()
		inputRows, outputRows, outputBytes := 0, 0, 0
		matchedRight := make([]bool, len(join.right.table.Rows))
		if err := left(func(row relationRow) error {
			inputRows += 1 + len(join.right.table.Rows)
			rightStarted := time.Now()
			err := yieldJoinedRows(row, join, matchedRight, func(row relationRow) error {
				outputRows++
				outputBytes += relationRowMemory(row)
				return yield(row)
			})
			runtime.recordSourceScan(leftTables, join.right, len(join.right.table.Rows), sourceRowBytes(join.right.table.Rows), time.Since(rightStarted))
			return err
		}); err != nil {
			return err
		}
		if join.kind != "right" {
			runtime.recordJoin(leftTables-1, inputRows, outputRows, outputBytes, time.Since(started))
			return nil
		}
		if inputRows == 0 {
			runtime.recordSourceScan(leftTables, join.right, len(join.right.table.Rows), sourceRowBytes(join.right.table.Rows), time.Since(started))
		}
		err := yieldUnmatchedRight(join.right.table.Rows, matchedRight, leftWidth, leftTables, func(row relationRow) error {
			outputRows++
			outputBytes += relationRowMemory(row)
			return yield(row)
		})
		runtime.recordJoin(leftTables-1, inputRows, outputRows, outputBytes, time.Since(started))
		return err
	}
}

func hashJoinedRowIterator(left relationRowIterator, join relationalJoin, leftWidth, leftTables int, runtime *selectRuntimeBinding) relationRowIterator {
	return func(yield relationRowYield) error {
		return runHashJoin(left, join, leftWidth, leftTables, runtime, yield)
	}
}

type hashJoinRun struct {
	join                   relationalJoin
	buckets                map[string][]int
	matchedRight           []bool
	yield                  relationRowYield
	inputRows, outputRows  int
	outputBytes, leftWidth int
	leftTables             int
}

func runHashJoin(left relationRowIterator, join relationalJoin, leftWidth, leftTables int, runtime *selectRuntimeBinding, yield relationRowYield) error {
	started, rightStarted := time.Now(), time.Now()
	buckets, hashBytes, err := buildRightHashBuckets(join, leftWidth)
	runtime.recordSourceScan(leftTables, join.right, len(join.right.table.Rows), sourceRowBytes(join.right.table.Rows), time.Since(rightStarted))
	if err != nil {
		return err
	}
	run := &hashJoinRun{
		join: join, buckets: buckets, matchedRight: make([]bool, len(join.right.table.Rows)),
		yield: yield, inputRows: len(join.right.table.Rows), leftWidth: leftWidth, leftTables: leftTables,
	}
	err = left(run.yieldLeftRow)
	if err == nil {
		err = run.yieldRightRemainder()
	}
	runtime.recordJoin(leftTables-1, run.inputRows, run.outputRows, hashBytes+run.outputBytes, time.Since(started))
	return err
}

func (run *hashJoinRun) yieldLeftRow(row relationRow) error {
	run.inputRows++
	key, present, err := relationalHashKey(run.join.hash.left, run.join.hash.comparison, row)
	if err != nil {
		return err
	}
	matched := false
	if present {
		matched, err = run.yieldMatches(row, run.buckets[key])
	}
	if err != nil || matched || run.join.kind != "left" {
		return err
	}
	return run.emit(appendNullRight(row, len(run.join.right.columns)))
}

func (run *hashJoinRun) yieldMatches(row relationRow, indexes []int) (bool, error) {
	matched := false
	for _, rightIndex := range indexes {
		candidate := joinedCandidate(row, run.join.right.table.Rows[rightIndex])
		ok, err := joinCandidateMatches(run.join, candidate)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		matched, run.matchedRight[rightIndex] = true, true
		if err := run.emit(candidate); err != nil {
			return false, err
		}
	}
	return matched, nil
}

func (run *hashJoinRun) yieldRightRemainder() error {
	if run.join.kind != "right" {
		return nil
	}
	return yieldUnmatchedRight(run.join.right.table.Rows, run.matchedRight, run.leftWidth, run.leftTables, run.emit)
}

func (run *hashJoinRun) emit(row relationRow) error {
	run.outputRows++
	run.outputBytes += relationRowMemory(row)
	return run.yield(row)
}

func buildRightHashBuckets(join relationalJoin, leftWidth int) (map[string][]int, int, error) {
	buckets := make(map[string][]int, len(join.right.table.Rows))
	bytes := 0
	for index, values := range join.right.table.Rows {
		row := relationRow{values: make([]string, leftWidth, leftWidth+len(values))}
		row.values = append(row.values, values...)
		key, present, err := relationalHashKey(join.hash.right, join.hash.comparison, row)
		if err != nil {
			return nil, 0, err
		}
		if !present {
			continue
		}
		buckets[key] = append(buckets[key], index)
		bytes += len(key) + 8
	}
	return buckets, bytes, nil
}

func relationalHashKey(operand relationOperand, comparison characterType, row relationRow) (string, bool, error) {
	value, err := relationOperandValue(operand, row)
	if err != nil || value.isNull() {
		return "", false, err
	}
	key := value.render()
	if value.kind == valueString {
		key = characterComparisonKey(comparison, key)
	}
	return strconv.Itoa(int(value.kind)) + ":" + key, true, nil
}

func joinedCandidate(left relationRow, right []string) relationRow {
	return relationRow{
		values:   append(append([]string(nil), left.values...), right...),
		lockKeys: append(append([]string(nil), left.lockKeys...), rowLockKey(right)),
	}
}

func relationRowMemory(row relationRow) int {
	bytes := 0
	for _, value := range row.values {
		bytes += len(value)
	}
	for _, key := range row.lockKeys {
		bytes += len(key)
	}
	return bytes
}

func sourceRowBytes(rows [][]string) int {
	bytes := 0
	for _, row := range rows {
		for _, value := range row {
			bytes += len(value)
		}
	}
	return bytes
}

func yieldJoinedRows(left relationRow, join relationalJoin, matchedRight []bool, yield relationRowYield) error {
	matched := false
	for rightIndex, values := range join.right.table.Rows {
		candidate := joinedCandidate(left, values)
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
