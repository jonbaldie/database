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
	lower := strings.ToLower(query)
	switch {
	case lower == "begin", lower == "start transaction", lower == "commit", lower == "rollback":
		return Result{}
	case strings.HasPrefix(lower, "create database ") || strings.HasPrefix(lower, "create schema "):
		return e.createNamespace(query)
	case strings.HasPrefix(lower, "use "):
		return e.useNamespace(query)
	case strings.HasPrefix(lower, "create table "):
		return e.createTable(query)
	case strings.HasPrefix(lower, "insert "):
		return e.insert(query)
	case strings.HasPrefix(lower, "update "):
		return e.update(query)
	case strings.HasPrefix(lower, "delete "):
		return e.delete(query)
	case strings.HasPrefix(lower, "select "):
		return e.selectRows(query)
	case lower == "show databases":
		return e.showDatabases()
	case lower == "show tables":
		return e.showTables()
	default:
		return Result{Err: fmt.Errorf("unsupported query %q", query)}
	}
}

func (e *Engine) createNamespace(query string) Result {
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

func (e *Engine) useNamespace(query string) Result {
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

func (e *Engine) createTable(query string) Result {
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
	columns := make([]Column, 0)
	for _, definition := range splitTopLevel(query[open+1:close], ',') {
		fields := strings.Fields(strings.TrimSpace(definition))
		if len(fields) == 0 {
			continue
		}
		first := strings.ToLower(strings.Trim(fields[0], "`"))
		if first == "primary" || first == "unique" || first == "foreign" || first == "check" || first == "constraint" {
			continue
		}
		columns = append(columns, Column{Name: normalize(fields[0])})
	}
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

func (e *Engine) insert(query string) Result {
	lower := strings.ToLower(query)
	into := strings.Index(lower, "into ")
	valuesAt := strings.Index(lower, " values")
	if into < 0 || valuesAt < 0 {
		return Result{Err: errors.New("invalid insert statement")}
	}
	target := strings.TrimSpace(query[into+5 : valuesAt])
	columns := []string{}
	if open := strings.Index(target, "("); open >= 0 {
		close := strings.LastIndex(target, ")")
		if close < open {
			return Result{Err: errors.New("invalid insert columns")}
		}
		columns = splitTopLevel(target[open+1:close], ',')
		target = strings.TrimSpace(target[:open])
	}
	name := normalize(target)
	groups := parseValueGroups(query[valuesAt+7:])
	if len(groups) == 0 {
		return Result{Err: errors.New("insert values are required")}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	original := e.namespaces[e.current].Tables[name]
	if original == nil {
		return Result{Err: errors.New("unknown table")}
	}
	copyTable := cloneTable(original)
	if len(columns) == 0 {
		for _, c := range copyTable.Columns {
			columns = append(columns, c.Name)
		}
	}
	indexes := make(map[string]int, len(copyTable.Columns))
	for i, c := range copyTable.Columns {
		indexes[normalize(c.Name)] = i
	}
	for _, group := range groups {
		if len(group) != len(columns) {
			return Result{Err: errors.New("column count does not match value count")}
		}
		row := make([]string, len(copyTable.Columns))
		for i, col := range columns {
			index, ok := indexes[normalize(col)]
			if !ok {
				return Result{Err: fmt.Errorf("unknown column %q", col)}
			}
			row[index] = unquote(group[i])
		}
		copyTable.Rows = append(copyTable.Rows, row)
	}
	e.namespaces[e.current].Tables[name] = copyTable
	return Result{Affected: uint64(len(groups))}
}

func (e *Engine) update(query string) Result {
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
			bits := strings.SplitN(assignment, "=", 2)
			if len(bits) != 2 {
				return Result{Err: errors.New("invalid assignment")}
			}
			index, ok := indexes[normalize(bits[0])]
			if !ok {
				return Result{Err: errors.New("unknown column")}
			}
			copyTable.Rows[i][index] = unquote(strings.TrimSpace(bits[1]))
		}
	}
	e.namespaces[e.current].Tables[tableName] = copyTable
	return Result{Affected: uint64(len(copyTable.Rows))}
}

func (e *Engine) delete(query string) Result {
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

func (e *Engine) selectRows(query string) Result {
	lower := strings.ToLower(query)
	fromAt := strings.Index(lower, " from ")
	if fromAt < 0 {
		return selectLiteral(strings.TrimSpace(query[7:]))
	}
	projection := strings.TrimSpace(query[7:fromAt])
	rest := query[fromAt+6:]
	restLower := strings.ToLower(rest)
	positions := []int{}
	for _, keyword := range []string{" where ", " order by ", " limit "} {
		if p := strings.Index(restLower, keyword); p >= 0 {
			positions = append(positions, p)
		}
	}
	clauseEnd := len(rest)
	for _, p := range positions {
		if p < clauseEnd {
			clauseEnd = p
		}
	}
	tableName := normalize(strings.TrimSpace(rest[:clauseEnd]))
	whereText, orderText, limitText := "", "", ""
	if p := strings.Index(restLower, " where "); p >= 0 {
		end := len(rest)
		for _, k := range []string{" order by ", " limit "} {
			if q := strings.Index(restLower[p+7:], k); q >= 0 && p+7+q < end {
				end = p + 7 + q
			}
		}
		whereText = strings.TrimSpace(rest[p+7 : end])
	}
	if p := strings.Index(restLower, " order by "); p >= 0 {
		end := len(rest)
		if q := strings.Index(restLower[p+10:], " limit "); q >= 0 {
			end = p + 10 + q
		}
		orderText = strings.TrimSpace(rest[p+10 : end])
	}
	if p := strings.Index(restLower, " limit "); p >= 0 {
		limitText = strings.TrimSpace(rest[p+7:])
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	table := e.namespaces[e.current].Tables[tableName]
	if table == nil {
		return Result{Err: errors.New("unknown table")}
	}
	indexes := columnIndexes(table)
	selected := []int{}
	columns := []string{}
	if projection == "*" {
		for i, c := range table.Columns {
			selected = append(selected, i)
			columns = append(columns, c.Name)
		}
	} else {
		for _, name := range splitTopLevel(projection, ',') {
			index, ok := indexes[normalize(name)]
			if !ok {
				return Result{Err: errors.New("unknown column")}
			}
			selected = append(selected, index)
			columns = append(columns, table.Columns[index].Name)
		}
	}
	rows := [][]string{}
	for _, row := range table.Rows {
		if whereText != "" && !matches(row, indexes, whereText) {
			continue
		}
		projected := make([]string, len(selected))
		for i, index := range selected {
			projected[i] = row[index]
		}
		rows = append(rows, projected)
	}
	if orderText != "" {
		orderBits := strings.Fields(orderText)
		index := indexes[normalize(orderBits[0])]
		sort.SliceStable(rows, func(i, j int) bool { return rows[i][index] < rows[j][index] })
		if len(orderBits) > 1 && strings.EqualFold(orderBits[1], "desc") {
			sort.SliceStable(rows, func(i, j int) bool { return rows[i][index] > rows[j][index] })
		}
	}
	if limitText != "" {
		if limit, err := strconv.Atoi(strings.Fields(limitText)[0]); err == nil && limit >= 0 && len(rows) > limit {
			rows = rows[:limit]
		}
	}
	return Result{Columns: columns, Rows: rows}
}

func (e *Engine) showDatabases() Result {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rows := [][]string{}
	for name := range e.namespaces {
		rows = append(rows, []string{name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return Result{Columns: []string{"Database"}, Rows: rows}
}
func (e *Engine) showTables() Result {
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
func splitTopLevel(s string, separator rune) []string {
	result := []string{}
	start, depth := 0, 0
	quote := rune(0)
	for i, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '(' {
			depth++
		}
		if r == ')' {
			depth--
		}
		if r == separator && depth == 0 {
			result = append(result, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	result = append(result, strings.TrimSpace(s[start:]))
	return result
}
func parseValueGroups(s string) [][]string {
	groups := [][]string{}
	for i := 0; i < len(s); {
		open := strings.IndexByte(s[i:], '(')
		if open < 0 {
			break
		}
		open += i
		depth, quote, close := 0, byte(0), -1
		for j := open; j < len(s); j++ {
			c := s[j]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				continue
			}
			if c == '(' {
				depth++
			}
			if c == ')' {
				depth--
				if depth == 0 {
					close = j
					break
				}
			}
		}
		if close < 0 {
			break
		}
		groups = append(groups, splitTopLevel(s[open+1:close], ','))
		i = close + 1
	}
	return groups
}
