package queryexplanation

import (
	"encoding/json"
)

// Select describes a supported read to be explained.
type Select struct {
	Table      Table
	Columns    []string // resolved output columns
	AllColumns bool     // projection was '*'
	Where      string   // predicate fragment without the WHERE keyword, "" if absent
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
	root := tableScan(read.Table)
	if read.Where != "" {
		root = whereFilter(read.Where, root)
	}
	if !read.AllColumns {
		root = projection(read.Table, read.Columns, root)
	}
	return root
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
