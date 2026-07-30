package queryexplanation

import (
	"encoding/json"
)

// Select describes a supported read to be explained.
type Select struct {
	Table                 Table
	Tables                []Table
	Joins                 []Join
	Columns               []string // resolved output columns
	ProjectionExpressions []string // source SQL expressions, when distinct from output names
	AllColumns            bool     // projection was '*'
	Where                 string   // predicate fragment without the WHERE keyword, "" if absent
	GroupExpressions      []string // GROUP BY expressions
	Having                string   // HAVING predicate fragment without the keyword
	AggregateCount        int      // aggregate expressions in the SELECT list
	Window                WindowDetails
	Distinct              bool
	Orders                []Order
	Limit                 Limit
}

// Join records one source relation and its SQL join predicate.
type Join struct {
	Type           string
	Table          Table
	Condition      string
	SourceClause   string
	SourceFragment string
}

// Order records one ORDER BY expression and direction.
type Order struct {
	Expression string `json:"expression"`
	Direction  string `json:"direction"`
}

// Window records the SQL-visible definition and functions for one window.
type Window struct {
	PartitionExpressions []string `json:"partition_expressions"`
	Orders               []Order  `json:"orders"`
	Frame                string   `json:"frame"`
	Functions            []string `json:"functions"`
}

// WindowDetails records the resolved window work in a SELECT list.
type WindowDetails struct {
	Count         int      // distinct window definitions in the SELECT list
	FunctionCount int      // window functions in the SELECT list
	Definitions   []Window // resolved window definitions and functions
}

// Limit records the bounded row window requested by LIMIT.
type Limit struct {
	Present bool
	Offset  int
	Count   int
}

// Write describes a supported insert, update, or delete to be explained.
type Write struct {
	Kind      string // insert, update, or delete
	Table     Table
	ValueRows int    // number of literal rows, for insert
	Where     string // predicate fragment without the WHERE keyword, for update and delete
}

// PlanSelect returns the plan-only explanation document for a supported read.
func PlanSelect(serverVersion, sql, currentDatabase string, read Select) *Document {
	statement := Statement{
		Kind:        "select",
		SQL:         sql,
		ReadOnly:    true,
		LockingRead: false,
	}
	return newDocument(serverVersion, currentDatabase, statement, selectPlan(read))
}

// PlanScalarSelect returns the plan-only explanation for a supported read that
// has no FROM clause and therefore produces one literal row.
func PlanScalarSelect(serverVersion, sql, currentDatabase string, columns []string) *Document {
	statement := Statement{Kind: "select", SQL: sql, ReadOnly: true, LockingRead: false}
	root := &Operator{
		Kind:      "values",
		Summary:   "Produce the single literal result row.",
		Operation: valuesOperation{RowCount: 1},
		Estimates: Estimates{Rows: 1, RowWidthBytes: rowWidth(len(columns)), Cost: 0, PeakMemoryBytes: 0},
		Output:    Output{Columns: append([]string(nil), columns...), Ordering: []OrderingTerm{}, UniqueKeys: [][]string{}},
		Warnings:  []Warning{},
		Children:  []*Operator{},
	}
	return newDocument(serverVersion, currentDatabase, statement, root)
}

// PlanWrite returns the plan-only explanation document for a supported write.
func PlanWrite(serverVersion, sql, currentDatabase string, write Write) *Document {
	statement := Statement{
		Kind:        write.Kind,
		SQL:         sql,
		ReadOnly:    false,
		LockingRead: false,
	}
	return newDocument(serverVersion, currentDatabase, statement, writePlan(write))
}

func newDocument(serverVersion, currentDatabase string, statement Statement, root *Operator) *Document {
	statement.CurrentDatabase = currentDatabase
	statement.Parameters = []Parameter{}
	statement.PlanningSettings = map[string]string{"sql_mode": FixedSQLMode}
	assignIdentifiers(root, new(int))
	return &Document{
		FormatVersion: FormatVersion,
		ServerVersion: serverVersion,
		Mode:          "plan",
		Partial:       false,
		Statement:     statement,
		Timing:        Timing{PlanningMS: 0},
		Plan:          root,
		Warnings:      []Warning{},
	}
}

func assignIdentifiers(operator *Operator, counter *int) {
	*counter++
	operator.ID = *counter
	for _, child := range operator.Children {
		assignIdentifiers(child, counter)
	}
}

func selectPlan(read Select) *Operator {
	tables := selectTables(read)
	root := selectSource(read, tables)
	root = selectFilter(read, root)
	root = selectGrouping(read, root)
	return selectOutput(read, tables, root)
}

func selectTables(read Select) []Table {
	if len(read.Tables) > 0 {
		return read.Tables
	}
	return []Table{read.Table}
}

func selectSource(read Select, tables []Table) *Operator {
	root := tableScan(tables[0])
	for _, join := range read.Joins {
		root = joinOperator(join, root, tableScan(join.Table))
	}
	return root
}

func selectFilter(read Select, root *Operator) *Operator {
	if read.Where == "" {
		return root
	}
	return whereFilter(read.Where, root)
}

func selectGrouping(read Select, root *Operator) *Operator {
	if read.AggregateCount > 0 || len(read.GroupExpressions) > 0 {
		root = aggregateOperator(read, root)
	}
	if read.Having != "" {
		root = havingFilter(read.Having, root)
	}
	if read.Window.FunctionCount > 0 {
		root = windowSortOperators(read.Window.Definitions, root)
		root = windowOperator(read, root)
	}
	return root
}

func windowSortOperators(windows []Window, root *Operator) *Operator {
	for _, window := range windows {
		if len(window.Orders) > 0 {
			root = windowSortOperator(window.Orders, root)
		}
	}
	return root
}

func selectOutput(read Select, tables []Table, root *Operator) *Operator {
	root = selectProjection(read, tables, root)
	if read.Distinct {
		root = distinctOperator(root)
	}
	if len(read.Orders) > 0 {
		root = sortOperator(read.Orders, root)
	}
	if read.Limit.Present {
		root = limitOperator(read.Limit, root)
	}
	return root
}

func aggregateOperator(read Select, child *Operator) *Operator {
	scope := "global"
	if len(read.GroupExpressions) > 0 {
		scope = "grouped"
	}
	rows := child.Estimates.Rows
	if scope == "global" {
		rows = 1
	}
	return &Operator{
		Kind: "aggregate", Summary: "Combine input rows into aggregate result groups.",
		Operation: aggregateOperation{Scope: scope, AggregateCount: read.AggregateCount, GroupingExpressions: append([]string(nil), read.GroupExpressions...)},
		Estimates: Estimates{Rows: rows, RowWidthBytes: child.Estimates.RowWidthBytes, Cost: child.Estimates.Cost + child.Estimates.Rows, PeakMemoryBytes: child.Estimates.RowWidthBytes * int(child.Estimates.Rows)},
		Output:    child.Output, Warnings: []Warning{}, Children: []*Operator{child},
	}
}

func havingFilter(having string, child *Operator) *Operator {
	predicate := Predicate{Role: "having", Expression: having, Sources: []PredicateSource{{Clause: "having", Fragment: having}}}
	return &Operator{
		Kind: "filter", Summary: "Keep only aggregate groups matching the HAVING predicate.",
		Operation: filterOperation{Role: "having"}, Predicates: []Predicate{predicate},
		Estimates: Estimates{Rows: child.Estimates.Rows, RowWidthBytes: child.Estimates.RowWidthBytes, Cost: child.Estimates.Cost + child.Estimates.Rows, PeakMemoryBytes: 0},
		Output:    child.Output, Warnings: []Warning{}, Children: []*Operator{child},
	}
}

func windowOperator(read Select, child *Operator) *Operator {
	return &Operator{
		Kind: "window", Summary: "Evaluate window functions over ordered partitions.",
		Operation: windowOperation{WindowCount: read.Window.Count, FunctionCount: read.Window.FunctionCount, Windows: append([]Window(nil), read.Window.Definitions...)},
		Estimates: Estimates{Rows: child.Estimates.Rows, RowWidthBytes: child.Estimates.RowWidthBytes, Cost: child.Estimates.Cost + child.Estimates.Rows, PeakMemoryBytes: child.Estimates.RowWidthBytes * int(child.Estimates.Rows)},
		Output:    child.Output, Warnings: []Warning{}, Children: []*Operator{child},
	}
}

func selectProjection(read Select, tables []Table, child *Operator) *Operator {
	if read.AllColumns {
		return child
	}
	if len(read.ProjectionExpressions) > 0 {
		return projectionColumns(read.ProjectionExpressions, child)
	}
	if len(tables) == 1 {
		return projection(tables[0], read.Columns, child)
	}
	return projectionColumns(read.Columns, child)
}

func joinOperator(join Join, left, right *Operator) *Operator {
	joinType := join.Type
	if joinType == "" {
		joinType = "inner"
	}
	predicates := []Predicate{}
	if join.Condition != "" {
		clause, fragment := join.SourceClause, join.SourceFragment
		if clause == "" {
			clause = "on"
		}
		if fragment == "" {
			fragment = join.Condition
		}
		predicates = append(predicates, Predicate{
			Role: "join", Expression: join.Condition,
			Sources: []PredicateSource{{Clause: clause, Fragment: fragment}},
		})
	}
	rows := left.Estimates.Rows * right.Estimates.Rows
	return &Operator{
		Kind: "join", Summary: "Combine rows from the joined relations.",
		Operation: joinOperation{JoinType: joinType}, Predicates: predicates,
		Objects:   append(append([]ObjectReference{}, left.Objects...), right.Objects...),
		Estimates: Estimates{Rows: rows, RowWidthBytes: left.Estimates.RowWidthBytes + right.Estimates.RowWidthBytes, Cost: left.Estimates.Cost + right.Estimates.Cost + rows, PeakMemoryBytes: 0},
		Output:    concatenateOutput(left.Output, right.Output), Warnings: []Warning{}, Children: []*Operator{left, right},
	}
}

func projectionColumns(columns []string, child *Operator) *Operator {
	output := child.Output
	output.Columns = append([]string(nil), columns...)
	return &Operator{
		Kind: "project", Summary: "Keep only the requested output columns.",
		Operation: projectOperation{ExpressionCount: len(columns)},
		Estimates: Estimates{Rows: child.Estimates.Rows, RowWidthBytes: rowWidth(len(columns)), Cost: child.Estimates.Cost, PeakMemoryBytes: 0},
		Output:    output, Warnings: []Warning{}, Children: []*Operator{child},
	}
}

func concatenateOutput(left, right Output) Output {
	return Output{
		Columns:  append(append([]string(nil), left.Columns...), right.Columns...),
		Ordering: []OrderingTerm{}, UniqueKeys: [][]string{},
	}
}

func distinctOperator(child *Operator) *Operator {
	return &Operator{
		Kind: "distinct", Summary: "Remove duplicate result rows.",
		Operation: distinctOperation{Scope: "row"},
		Estimates: child.Estimates, Output: child.Output, Warnings: []Warning{}, Children: []*Operator{child},
	}
}

func sortOperator(orders []Order, child *Operator) *Operator {
	ordering := make([]OrderingTerm, len(orders))
	for index, order := range orders {
		ordering[index] = OrderingTerm{Expression: order.Expression, Direction: order.Direction}
	}
	output := child.Output
	output.Ordering = ordering
	return &Operator{
		Kind: "sort", Summary: "Order result rows by the requested expressions.",
		Operation: sortOperation{Purpose: "order_by"},
		Estimates: Estimates{Rows: child.Estimates.Rows, RowWidthBytes: child.Estimates.RowWidthBytes, Cost: child.Estimates.Cost + child.Estimates.Rows, PeakMemoryBytes: child.Estimates.RowWidthBytes * int(child.Estimates.Rows)},
		Output:    output, Warnings: []Warning{}, Children: []*Operator{child},
	}
}

func windowSortOperator(orders []Order, child *Operator) *Operator {
	operator := sortOperator(orders, child)
	operator.Summary = "Order rows inside each window partition."
	operator.Operation = sortOperation{Purpose: "window"}
	operator.Output = child.Output
	return operator
}

func limitOperator(limit Limit, child *Operator) *Operator {
	rows := float64(limit.Count)
	if rows > child.Estimates.Rows {
		rows = child.Estimates.Rows
	}
	return &Operator{
		Kind: "limit", Summary: "Return only the requested row window.",
		Operation: limitOperation{OffsetPresent: limit.Offset > 0},
		Estimates: Estimates{Rows: rows, RowWidthBytes: child.Estimates.RowWidthBytes, Cost: child.Estimates.Cost, PeakMemoryBytes: child.Estimates.PeakMemoryBytes},
		Output:    child.Output, Warnings: []Warning{}, Children: []*Operator{child},
	}
}

func writePlan(write Write) *Operator {
	switch write.Kind {
	case "insert":
		return insertPlan(write)
	case "delete":
		return mutationOverScan(write, "delete", "Delete the matching rows from the table.")
	default:
		return mutationOverScan(write, "update", "Update the matching rows in place.")
	}
}

func insertPlan(write Write) *Operator {
	source := literalRows(write.Table, write.ValueRows)
	root := &Operator{
		Kind:      "mutation",
		Summary:   "Insert the submitted rows into the table.",
		Operation: mutationOperation{MutationType: "insert"},
		Objects:   []ObjectReference{tableObject(write.Table)},
		Estimates: mutationEstimates(write.Table, source.Estimates.Rows, source.Estimates.Cost),
		Output:    emptyOutput(),
		Warnings:  []Warning{},
		Children:  []*Operator{source},
	}
	return root
}

func mutationOverScan(write Write, mutationType, summary string) *Operator {
	source := tableScan(write.Table)
	if write.Where != "" {
		source = whereFilter(write.Where, source)
	}
	return &Operator{
		Kind:      "mutation",
		Summary:   summary,
		Operation: mutationOperation{MutationType: mutationType},
		Objects:   []ObjectReference{tableObject(write.Table)},
		Estimates: mutationEstimates(write.Table, source.Estimates.Rows, source.Estimates.Cost),
		Output:    emptyOutput(),
		Warnings:  []Warning{},
		Children:  []*Operator{source},
	}
}

func tableScan(table Table) *Operator {
	rows := float64(table.RowCount)
	return &Operator{
		Kind:      "scan",
		Summary:   "Read every row of the table in stored order.",
		Operation: scanOperation{Source: "table", Direction: "forward"},
		Strategy:  &Strategy{Name: "full_table_scan", Summary: "Sequentially read all stored rows."},
		Objects:   []ObjectReference{tableObject(table)},
		Estimates: Estimates{Rows: rows, RowWidthBytes: rowWidth(len(table.Columns)), Cost: rows + 1, PeakMemoryBytes: 0},
		Output:    columnOutput(table, table.Columns),
		Warnings:  []Warning{},
		Children:  []*Operator{},
	}
}

func whereFilter(where string, child *Operator) *Operator {
	predicate := Predicate{
		Role:       "residual",
		Expression: where,
		Sources:    []PredicateSource{{Clause: "where", Fragment: where}},
	}
	return &Operator{
		Kind:       "filter",
		Summary:    "Keep only the rows matching the WHERE predicate.",
		Operation:  filterOperation{Role: "where"},
		Predicates: []Predicate{predicate},
		Estimates:  Estimates{Rows: child.Estimates.Rows, RowWidthBytes: child.Estimates.RowWidthBytes, Cost: child.Estimates.Cost + child.Estimates.Rows, PeakMemoryBytes: 0},
		Output:     child.Output,
		Warnings:   []Warning{},
		Children:   []*Operator{child},
	}
}

func literalRows(table Table, count int) *Operator {
	rows := float64(count)
	return &Operator{
		Kind:      "values",
		Summary:   "Produce the submitted rows to be written.",
		Operation: valuesOperation{RowCount: count},
		Estimates: Estimates{Rows: rows, RowWidthBytes: rowWidth(len(table.Columns)), Cost: 0, PeakMemoryBytes: 0},
		Output:    columnOutput(table, table.Columns),
		Warnings:  []Warning{},
		Children:  []*Operator{},
	}
}

func projection(table Table, columns []string, child *Operator) *Operator {
	return &Operator{
		Kind:      "project",
		Summary:   "Keep only the requested output columns.",
		Operation: projectOperation{ExpressionCount: len(columns)},
		Estimates: Estimates{Rows: child.Estimates.Rows, RowWidthBytes: rowWidth(len(columns)), Cost: child.Estimates.Cost, PeakMemoryBytes: 0},
		Output:    columnOutput(table, columns),
		Warnings:  []Warning{},
		Children:  []*Operator{child},
	}
}

func tableObject(table Table) ObjectReference {
	return ObjectReference{Type: "table", Database: table.Database, Name: table.Name}
}

func mutationEstimates(table Table, rows, childCost float64) Estimates {
	return Estimates{Rows: rows, RowWidthBytes: rowWidth(len(table.Columns)), Cost: childCost + rows + 1, PeakMemoryBytes: 0}
}

// RenderJSON returns the canonical single-document JSON encoding.
func RenderJSON(document *Document) (string, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
