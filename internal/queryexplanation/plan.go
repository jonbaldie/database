package queryexplanation

import (
	"encoding/json"
)

// Select describes a supported read to be explained.
type Select struct {
	Table      Table
	Tables     []Table
	Joins      []Join
	Columns    []string // resolved output columns
	AllColumns bool     // projection was '*'
	Where      string   // predicate fragment without the WHERE keyword, "" if absent
	Distinct   bool
	Orders     []Order
	Limit      Limit
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
	Expression string
	Direction  string
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
	tables := read.Tables
	if len(tables) == 0 {
		tables = []Table{read.Table}
	}
	root := tableScan(tables[0])
	for _, join := range read.Joins {
		root = joinOperator(join, root, tableScan(join.Table))
	}
	if read.Where != "" {
		root = whereFilter(read.Where, root)
	}
	if !read.AllColumns {
		if len(tables) == 1 {
			root = projection(tables[0], read.Columns, root)
		} else {
			root = projectionColumns(read.Columns, root)
		}
	}
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
