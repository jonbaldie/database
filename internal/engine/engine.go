// Package engine implements the relational state used by the server's public
// protocol. Its storage representation is intentionally private.
package engine

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Result struct {
	Columns  []string
	Rows     [][]string
	Affected uint64
	Err      error
}

type Column struct{ Name string }
type Table struct {
	Columns []Column
	Rows    [][]string
}
type Namespace struct{ Tables map[string]*Table }

type Engine struct {
	mu         sync.RWMutex
	namespaces map[string]*Namespace
	current    string
}

func New() *Engine {
	return &Engine{
		namespaces: map[string]*Namespace{"test": {Tables: map[string]*Table{}}},
		current:    "test",
	}
}

func (e *Engine) Execute(query string) Result {
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return Result{Err: errors.New("empty statement")}
	}
	if handler := queryHandler(strings.ToLower(query)); handler != nil {
		return handler(e, query)
	}
	return Result{Err: fmt.Errorf("unsupported query %q", query)}
}

type queryHandlerFunc func(*Engine, string) Result

var exactQueryHandlers = map[string]queryHandlerFunc{
	"begin":             ignoreTransaction,
	"start transaction": ignoreTransaction,
	"commit":            ignoreTransaction,
	"rollback":          ignoreTransaction,
	"show databases":    func(e *Engine, _ string) Result { return showDatabases(e) },
	"show tables":       func(e *Engine, _ string) Result { return showTables(e) },
}

var prefixQueryHandlers = []struct {
	prefix  string
	handler queryHandlerFunc
}{
	{"create database ", createNamespace}, {"create schema ", createNamespace},
	{"use ", useNamespace}, {"create table ", createTable}, {"insert ", insert},
	{"update ", update}, {"delete ", deleteRows}, {"select ", selectRows},
}

func queryHandler(lower string) queryHandlerFunc {
	if handler := exactQueryHandlers[lower]; handler != nil {
		return handler
	}
	for _, candidate := range prefixQueryHandlers {
		if strings.HasPrefix(lower, candidate.prefix) {
			return candidate.handler
		}
	}
	return nil
}

func ignoreTransaction(_ *Engine, _ string) Result { return Result{} }

func createNamespace(e *Engine, query string) Result {
	parts := strings.Fields(query)
	if len(parts) < 3 {
		return Result{Err: errors.New("namespace name is required")}
	}
	name := normalize(parts[2])
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.namespaces[name]; ok {
		return Result{Err: errors.New("namespace already exists")}
	}
	e.namespaces[name] = &Namespace{Tables: map[string]*Table{}}
	return Result{Affected: 1}
}

func useNamespace(e *Engine, query string) Result {
	parts := strings.Fields(query)
	if len(parts) != 2 {
		return Result{Err: errors.New("namespace name is required")}
	}
	name := normalize(parts[1])
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.namespaces[name]; !ok {
		return Result{Err: errors.New("unknown database")}
	}
	e.current = name
	return Result{}
}

func createTable(e *Engine, query string) Result {
	open := strings.Index(query, "(")
	close := strings.LastIndex(query, ")")
	if open < 0 || close <= open {
		return Result{Err: errors.New("table definition is required")}
	}
	prefix := strings.TrimSpace(query[:open])
	parts := strings.Fields(prefix)
	if len(parts) < 3 {
		return Result{Err: errors.New("table name is required")}
	}
	name := normalize(parts[2])
	columns := tableColumns(query[open+1 : close])
	if len(columns) == 0 {
		return Result{Err: errors.New("at least one column is required")}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ns := e.namespaces[e.current]
	if _, ok := ns.Tables[name]; ok {
		return Result{Err: errors.New("table already exists")}
	}
	ns.Tables[name] = &Table{Columns: columns}
	return Result{Affected: 1}
}

func tableColumns(definitions string) []Column {
	columns := make([]Column, 0)
	for _, definition := range splitTopLevel(definitions, ',') {
		if column, ok := columnDefinition(definition); ok {
			columns = append(columns, column)
		}
	}
	return columns
}

func columnDefinition(definition string) (Column, bool) {
	fields := strings.Fields(strings.TrimSpace(definition))
	if len(fields) == 0 || tableConstraint(fields[0]) {
		return Column{}, false
	}
	return Column{Name: normalize(fields[0])}, true
}

func tableConstraint(name string) bool {
	switch strings.ToLower(strings.Trim(name, "`")) {
	case "primary", "unique", "foreign", "check", "constraint":
		return true
	default:
		return false
	}
}

func insert(e *Engine, query string) Result {
	name, columns, groups, err := insertParts(query)
	if err != nil {
		return Result{Err: err}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	original := e.namespaces[e.current].Tables[name]
	if original == nil {
		return Result{Err: errors.New("unknown table")}
	}
	copyTable := cloneTable(original)
	if err := insertRows(copyTable, columns, groups); err != nil {
		return Result{Err: err}
	}
	e.namespaces[e.current].Tables[name] = copyTable
	return Result{Affected: uint64(len(groups))}
}

func insertParts(query string) (string, []string, [][]string, error) {
	lower := strings.ToLower(query)
	into, valuesAt := strings.Index(lower, "into "), strings.Index(lower, " values")
	if into < 0 || valuesAt < 0 {
		return "", nil, nil, errors.New("invalid insert statement")
	}
	target, columns, err := insertTarget(strings.TrimSpace(query[into+5 : valuesAt]))
	if err != nil {
		return "", nil, nil, err
	}
	groups := parseValueGroups(query[valuesAt+7:])
	if len(groups) == 0 {
		return "", nil, nil, errors.New("insert values are required")
	}
	return normalize(target), columns, groups, nil
}

func insertTarget(target string) (string, []string, error) {
	open := strings.Index(target, "(")
	if open < 0 {
		return target, nil, nil
	}
	close := strings.LastIndex(target, ")")
	if close < open {
		return "", nil, errors.New("invalid insert columns")
	}
	return strings.TrimSpace(target[:open]), splitTopLevel(target[open+1:close], ','), nil
}

func insertRows(table *Table, columns []string, groups [][]string) error {
	if len(columns) == 0 {
		columns = columnNames(table.Columns)
	}
	indexes := columnIndexes(table)
	for _, group := range groups {
		row, err := insertedRow(table.Columns, indexes, columns, group)
		if err != nil {
			return err
		}
		table.Rows = append(table.Rows, row)
	}
	return nil
}

func columnNames(columns []Column) []string {
	names := make([]string, len(columns))
	for index, column := range columns {
		names[index] = column.Name
	}
	return names
}

func insertedRow(columns []Column, indexes map[string]int, names, values []string) ([]string, error) {
	if len(values) != len(names) {
		return nil, errors.New("column count does not match value count")
	}
	row := make([]string, len(columns))
	for index, name := range names {
		column, ok := indexes[normalize(name)]
		if !ok {
			return nil, fmt.Errorf("unknown column %q", name)
		}
		row[column] = unquote(values[index])
	}
	return row, nil
}

func update(e *Engine, query string) Result {
	lower := strings.ToLower(query)
	setAt := strings.Index(lower, " set ")
	if setAt < 0 {
		return Result{Err: errors.New("update assignments are required")}
	}
	whereAt := strings.Index(lower[setAt+5:], " where ")
	whereAt = offset(whereAt, setAt+5)
	tableName := normalize(strings.TrimSpace(query[7:setAt]))
	assignmentText := query[setAt+5:]
	whereText := ""
	if whereAt >= 0 {
		whereText = query[whereAt+7:]
		assignmentText = query[setAt+5 : whereAt]
	}
	assignments := splitTopLevel(assignmentText, ',')
	e.mu.Lock()
	defer e.mu.Unlock()
	original := e.namespaces[e.current].Tables[tableName]
	if original == nil {
		return Result{Err: errors.New("unknown table")}
	}
	copyTable := cloneTable(original)
	indexes := columnIndexes(copyTable)
	for i, row := range copyTable.Rows {
		if whereText != "" && !matches(row, indexes, whereText) {
			continue
		}
		for _, assignment := range assignments {
			index, value, err := assignmentValue(indexes, assignment)
			if err != nil {
				return Result{Err: err}
			}
			copyTable.Rows[i][index] = value
		}
	}
	e.namespaces[e.current].Tables[tableName] = copyTable
	return Result{Affected: uint64(len(copyTable.Rows))}
}

func assignmentValue(indexes map[string]int, assignment string) (int, string, error) {
	bits := strings.SplitN(assignment, "=", 2)
	if len(bits) != 2 {
		return 0, "", errors.New("invalid assignment")
	}
	index, ok := indexes[normalize(bits[0])]
	if !ok {
		return 0, "", errors.New("unknown column")
	}
	return index, unquote(strings.TrimSpace(bits[1])), nil
}

func deleteRows(e *Engine, query string) Result {
	lower := strings.ToLower(query)
	fromAt := strings.Index(lower, "from ")
	if fromAt < 0 {
		return Result{Err: errors.New("delete table is required")}
	}
	whereAt := strings.Index(lower[fromAt+5:], " where ")
	whereAt = offset(whereAt, fromAt+5)
	name := normalize(strings.TrimSpace(query[fromAt+5:]))
	whereText := ""
	if whereAt >= 0 {
		name = normalize(strings.TrimSpace(query[fromAt+5 : whereAt]))
		whereText = query[whereAt+7:]
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	original := e.namespaces[e.current].Tables[name]
	if original == nil {
		return Result{Err: errors.New("unknown table")}
	}
	indexes := columnIndexes(original)
	kept := make([][]string, 0, len(original.Rows))
	affected := 0
	for _, row := range original.Rows {
		if whereText == "" || matches(row, indexes, whereText) {
			affected++
			continue
		}
		kept = append(kept, row)
	}
	original.Rows = kept
	return Result{Affected: uint64(affected)}
}

func selectRows(e *Engine, query string) Result {
	lower := strings.ToLower(query)
	fromAt := strings.Index(lower, " from ")
	if fromAt < 0 {
		return selectLiteral(strings.TrimSpace(query[7:]))
	}
	statement := parseSelect(query, fromAt)
	e.mu.RLock()
	defer e.mu.RUnlock()
	table := e.namespaces[e.current].Tables[statement.table]
	if table == nil {
		return Result{Err: errors.New("unknown table")}
	}
	indexes := columnIndexes(table)
	selected, columns, err := selectedColumns(table, indexes, statement.projection)
	if err != nil {
		return Result{Err: err}
	}
	rows := projectedRows(table.Rows, indexes, statement.where, selected)
	sortSelectedRows(rows, indexes, statement.order)
	rows = limitedRows(rows, statement.limit)
	return Result{Columns: columns, Rows: rows}
}

type selectStatement struct{ projection, table, where, order, limit string }

func parseSelect(query string, fromAt int) selectStatement {
	rest := query[fromAt+6:]
	lower := strings.ToLower(rest)
	return selectStatement{projection: strings.TrimSpace(query[7:fromAt]), table: normalize(strings.TrimSpace(rest[:firstClause(lower)])), where: clauseValue(rest, lower, " where ", []string{" order by ", " limit "}), order: clauseValue(rest, lower, " order by ", []string{" limit "}), limit: clauseValue(rest, lower, " limit ", nil)}
}

func firstClause(text string) int {
	end := len(text)
	for _, keyword := range []string{" where ", " order by ", " limit "} {
		if index := strings.Index(text, keyword); index >= 0 && index < end {
			end = index
		}
	}
	return end
}

func clauseValue(text, lower, marker string, endings []string) string {
	start := strings.Index(lower, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := len(text)
	for _, marker := range endings {
		if index := strings.Index(lower[start:], marker); index >= 0 && start+index < end {
			end = start + index
		}
	}
	return strings.TrimSpace(text[start:end])
}

func selectedColumns(table *Table, indexes map[string]int, projection string) ([]int, []string, error) {
	if projection == "*" {
		return allColumns(table.Columns)
	}
	selected, columns := []int{}, []string{}
	for _, name := range splitTopLevel(projection, ',') {
		index, ok := indexes[normalize(name)]
		if !ok {
			return nil, nil, errors.New("unknown column")
		}
		selected, columns = append(selected, index), append(columns, table.Columns[index].Name)
	}
	return selected, columns, nil
}

func allColumns(columns []Column) ([]int, []string, error) {
	selected, names := make([]int, len(columns)), make([]string, len(columns))
	for index, column := range columns {
		selected[index], names[index] = index, column.Name
	}
	return selected, names, nil
}

func projectedRows(source [][]string, indexes map[string]int, where string, selected []int) [][]string {
	rows := [][]string{}
	for _, row := range source {
		if where == "" || matches(row, indexes, where) {
			rows = append(rows, projectRow(row, selected))
		}
	}
	return rows
}

func projectRow(row []string, selected []int) []string {
	projected := make([]string, len(selected))
	for index, column := range selected {
		projected[index] = row[column]
	}
	return projected
}

func sortSelectedRows(rows [][]string, indexes map[string]int, order string) {
	if order == "" {
		return
	}
	bits := strings.Fields(order)
	index := indexes[normalize(bits[0])]
	descending := len(bits) > 1 && strings.EqualFold(bits[1], "desc")
	sort.SliceStable(rows, func(i, j int) bool { return orderedBefore(rows[i][index], rows[j][index], descending) })
}

func orderedBefore(left, right string, descending bool) bool {
	if descending {
		return left > right
	}
	return left < right
}

func limitedRows(rows [][]string, limitText string) [][]string {
	if limitText == "" {
		return rows
	}
	limit, err := strconv.Atoi(strings.Fields(limitText)[0])
	if err == nil && limit >= 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func showDatabases(e *Engine) Result {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rows := [][]string{}
	for name := range e.namespaces {
		rows = append(rows, []string{name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return Result{Columns: []string{"Database"}, Rows: rows}
}
func showTables(e *Engine) Result {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rows := [][]string{}
	for name := range e.namespaces[e.current].Tables {
		rows = append(rows, []string{name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return Result{Columns: []string{"Tables_in_" + e.current}, Rows: rows}
}

func selectLiteral(value string) Result {
	return Result{Columns: []string{"?column?"}, Rows: [][]string{{unquote(value)}}}
}
func cloneTable(t *Table) *Table {
	copy := &Table{Columns: append([]Column(nil), t.Columns...)}
	for _, row := range t.Rows {
		copy.Rows = append(copy.Rows, append([]string(nil), row...))
	}
	return copy
}
func columnIndexes(t *Table) map[string]int {
	result := map[string]int{}
	for i, c := range t.Columns {
		result[normalize(c.Name)] = i
	}
	return result
}
func matches(row []string, indexes map[string]int, expression string) bool {
	bits := strings.SplitN(strings.TrimSpace(expression), "=", 2)
	if len(bits) != 2 {
		return false
	}
	i, ok := indexes[normalize(bits[0])]
	return ok && row[i] == unquote(strings.TrimSpace(bits[1]))
}
func normalize(s string) string { return strings.ToLower(strings.Trim(strings.TrimSpace(s), "`")) }
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')) {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}
func offset(value, base int) int {
	if value < 0 {
		return -1
	}
	return value + base
}
func splitTopLevel(s string, separator byte) []string {
	result := []string{}
	start, depth := 0, 0
	quote := byte(0)
	for i, length := 0, len(s); i < length; i++ {
		character := s[i]
		if quote != 0 {
			quote = closingQuote(quote, character)
			continue
		}
		if quoteDelimiter(character) {
			quote = character
			continue
		}
		depth = parenthesisDepth(depth, character)
		if character == separator && depth == 0 {
			result = append(result, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	result = append(result, strings.TrimSpace(s[start:]))
	return result
}
func parseValueGroups(s string) [][]string {
	groups := [][]string{}
	for i, length := 0, len(s); i < length; {
		open := strings.IndexByte(s[i:], '(')
		if open < 0 {
			break
		}
		open += i
		close := closingParenthesis(s, open, length)
		if close < 0 {
			break
		}
		groups = append(groups, splitTopLevel(s[open+1:close], ','))
		i = close + 1
	}
	return groups
}

func closingParenthesis(text string, open, length int) int {
	depth, quote := 0, byte(0)
	for index := open; index < length; index++ {
		character := text[index]
		if quote != 0 {
			quote = closingQuote(quote, character)
			continue
		}
		if quoteDelimiter(character) {
			quote = character
			continue
		}
		depth = parenthesisDepth(depth, character)
		if depth == 0 {
			return index
		}
	}
	return -1
}

func closingQuote(quote, character byte) byte {
	if character == quote {
		return 0
	}
	return quote
}

func quoteDelimiter(character byte) bool { return character == '\'' || character == '"' }

func parenthesisDepth(depth int, character byte) int {
	if character == '(' {
		return depth + 1
	}
	if character == ')' {
		return depth - 1
	}
	return depth
}
