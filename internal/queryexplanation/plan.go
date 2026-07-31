package queryexplanation

import (
	"encoding/json"
	"strconv"
)

// Select describes a supported read to be explained.
type Select struct {
	// RuntimeKey identifies one executable SELECT term. It is only used to
	// attach observed counters to the matching physical operators.
	RuntimeKey            string
	Table                 Table
	Tables                []Table
	Joins                 []Join
	Columns               []string // resolved output columns
	ProjectionExpressions []string // source SQL expressions, when distinct from output names
	AllColumns            bool     // projection was '*'
	Where                 string   // predicate fragment without the WHERE keyword, "" if absent
	Aggregation           Aggregation
	Window                WindowDetails
	Distinct              bool
	Orders                []Order
	Limit                 Limit
	Locking               LockingRead
}

// Aggregation records SQL-visible grouped aggregate work in a SELECT list.
type Aggregation struct {
	GroupExpressions []string // GROUP BY expressions
	Having           string   // HAVING predicate fragment without the keyword
	Count            int      // aggregate expressions in the SELECT list
}

// LockingRead records whether a SELECT takes locks and the requested policy.
type LockingRead struct {
	Enabled    bool
	Mode       string
	WaitPolicy string
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

// Write describes a supported mutation to be explained.
type Write struct {
	Kind        string // insert, replace, update, or delete
	Table       Table
	ValueRows   int    // number of literal rows, for insert
	Where       string // predicate fragment without the WHERE keyword, for update and delete
	Constraints []Constraint
	Source      *Operator // planned INSERT ... SELECT input, when present
	Upsert      bool
}

// PlanSelect returns the plan-only explanation document for a supported read.
func PlanSelect(serverVersion, sql, currentDatabase string, read Select) *Document {
	statement := Statement{
		Kind:        "select",
		SQL:         sql,
		ReadOnly:    !read.Locking.Enabled,
		LockingRead: read.Locking.Enabled,
	}
	root := selectPlan(read)
	assignRuntimeKeys(root, read.RuntimeKey)
	return newDocument(serverVersion, currentDatabase, statement, root)
}

// RuntimeOperatorKey returns the private identity of one operator in a SELECT
// term. Callers must use the same term key and tree-order index that planning
// used. The key is never part of the JSON or tabular API.
func RuntimeOperatorKey(termKey, kind string, index int) string {
	return termKey + "/" + kind + "/" + strconv.Itoa(index)
}

func assignRuntimeKeys(root *Operator, termKey string) {
	if root == nil || termKey == "" {
		return
	}
	indexes := make(map[string]int)
	var visit func(*Operator)
	visit = func(operator *Operator) {
		if operator == nil {
			return
		}
		index := indexes[operator.Kind]
		operator.RuntimeKey = RuntimeOperatorKey(termKey, operator.Kind, index)
		indexes[operator.Kind] = index + 1
		for _, child := range operator.Children {
			visit(child)
		}
	}
	visit(root)
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
	if read.Aggregation.Count > 0 || len(read.Aggregation.GroupExpressions) > 0 {
		root = aggregateOperator(read, root)
	}
	if read.Aggregation.Having != "" {
		root = havingFilter(read.Aggregation.Having, root)
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
	if read.Locking.Enabled {
		root = lockInput(root, read.Locking.Mode, read.Locking.WaitPolicy)
	}
	return root
}

func aggregateOperator(read Select, child *Operator) *Operator {
	scope := "global"
	if len(read.Aggregation.GroupExpressions) > 0 {
		scope = "grouped"
	}
	rows := child.Estimates.Rows
	if scope == "global" {
		rows = 1
	}
	return &Operator{
		Kind: "aggregate", Summary: "Combine input rows into aggregate result groups.",
		Operation: aggregateOperation{Scope: scope, AggregateCount: read.Aggregation.Count, GroupingExpressions: append([]string(nil), read.Aggregation.GroupExpressions...)},
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

func lockInput(child *Operator, mode, waitPolicy string) *Operator {
	return &Operator{
		Kind:      "lock",
		Summary:   "Take row locks for the returned rows.",
		Operation: lockOperation{Mode: mode, WaitPolicy: waitPolicy},
		Estimates: child.Estimates,
		Output:    child.Output,
		Warnings:  []Warning{},
		Children:  []*Operator{child},
	}
}

func writePlan(write Write) *Operator {
	switch write.Kind {
	case "insert":
		if write.Upsert {
			return upsertPlan(write)
		}
		return insertPlan(write)
	case "replace":
		return replacePlan(write)
	case "delete":
		return mutationOverScan(write, "delete", "Delete the matching rows from the table.")
	default:
		return mutationOverScan(write, "update", "Update the matching rows in place.")
	}
}

func insertPlan(write Write) *Operator {
	source := writeSource(write)
	source = constraintChecks(write, source)
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

func upsertPlan(write Write) *Operator {
	source := constraintChecks(write, writeSource(write))
	return &Operator{
		Kind:      "mutation",
		Summary:   "Insert each submitted row or update its conflicting unique-key row.",
		Operation: mutationOperation{MutationType: "upsert"},
		Objects:   []ObjectReference{tableObject(write.Table)},
		Estimates: mutationEstimates(write.Table, source.Estimates.Rows, source.Estimates.Cost),
		Output:    emptyOutput(), Warnings: []Warning{}, Children: []*Operator{source},
	}
}

func replacePlan(write Write) *Operator {
	source := constraintChecks(write, writeSource(write))
	rows, cost := source.Estimates.Rows, source.Estimates.Cost
	deleteRows := &Operator{
		Kind: "mutation", Summary: "Delete rows that conflict with a primary or unique key.",
		Operation: mutationOperation{MutationType: "delete"}, Objects: []ObjectReference{tableObject(write.Table)},
		Estimates: mutationEstimates(write.Table, rows, cost), Output: emptyOutput(), Warnings: []Warning{}, Children: []*Operator{},
	}
	insertRows := &Operator{
		Kind: "mutation", Summary: "Insert the replacement rows after conflict deletion.",
		Operation: mutationOperation{MutationType: "insert"}, Objects: []ObjectReference{tableObject(write.Table)},
		Estimates: mutationEstimates(write.Table, rows, cost), Output: emptyOutput(), Warnings: []Warning{}, Children: []*Operator{},
	}
	return &Operator{
		Kind: "mutation", Summary: "Replace rows with delete-and-insert semantics for conflicting keys.",
		Operation: mutationOperation{MutationType: "replace"}, Objects: []ObjectReference{tableObject(write.Table)},
		Estimates: mutationEstimates(write.Table, rows, cost), Output: emptyOutput(), Warnings: []Warning{}, Children: []*Operator{source, deleteRows, insertRows},
	}
}

func writeSource(write Write) *Operator {
	if write.Source != nil {
		return write.Source
	}
	return literalRows(write.Table, write.ValueRows)
}

func mutationOverScan(write Write, mutationType, summary string) *Operator {
	source := tableScan(write.Table)
	if write.Where != "" {
		source = whereFilter(write.Where, source)
	}
	source = constraintChecks(write, source)
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

func constraintChecks(write Write, child *Operator) *Operator {
	for index := len(write.Constraints) - 1; index >= 0; index-- {
		constraint := write.Constraints[index]
		child = &Operator{
			Kind: "constraint_check", Summary: "Check the " + constraint.Name + " constraint.",
			Operation: constraintCheckOperation{ConstraintType: constraint.Type, ConstraintName: constraint.Name},
			Objects:   []ObjectReference{{Type: "constraint", Database: constraint.Table.Database, Table: constraint.Table.Name, Name: constraint.Name}},
			Estimates: child.Estimates, Output: child.Output, Warnings: []Warning{}, Children: []*Operator{child},
		}
	}
	return child
}

func tableScan(table Table) *Operator {
	if table.Access != nil {
		return btreeIndexScan(table)
	}
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

func btreeIndexScan(table Table) *Operator {
	rows := float64(table.RowCount)
	access := table.Access
	objects := []ObjectReference{tableObject(table), {Type: "index", Database: table.Database, Table: table.Name, Name: access.Name}}
	reason := CodeSummary{Code: "INDEX_SUPPORTS_QUERY", Summary: "The index supports the query filter or ordering."}
	if access.Forced {
		reason = CodeSummary{Code: "INDEX_HINT", Summary: "The statement requires this index through an index hint."}
	}
	strategy := &Strategy{Name: "btree_index_scan", Summary: "Traverse the selected B-tree index in key order."}
	summary := "Read rows through the selected B-tree index."
	if access.Covering {
		strategy = &Strategy{Name: "btree_covering_index_scan", Summary: "Traverse a B-tree index that contains every projected value."}
		summary = "Read the projected values through the selected covering B-tree index."
	}
	return &Operator{
		Kind:      "scan",
		Summary:   summary,
		Operation: scanOperation{Source: "index", Direction: "forward"},
		Strategy:  strategy,
		Objects:   objects,
		Choice: &Choice{Selected: access.Name, Reason: reason, Alternatives: []Alternative{{
			Name: "full_table_scan", EstimatedCost: rows + 1,
			Reason: CodeSummary{Code: "INDEX_NOT_SELECTED", Summary: "The alternative reads rows in stored table order."},
		}}},
		Statistics: []Statistic{{
			Object: tableObject(table), Kind: "catalog_row_count", CollectedAt: access.CollectedAt,
			ObservedRows: table.RowCount, Stale: false, Limitations: []string{"The statistic records the current catalog snapshot only."},
		}},
		Opportunities: []Opportunity{{
			Code: "INDEX_COVERAGE", Summary: "A covering index can reduce table-row reads.",
			Evidence: []string{"The selected path returns rows from the table after the index traversal."},
		}},
		Estimates: Estimates{Rows: rows, RowWidthBytes: rowWidth(len(table.Columns)), Cost: rows + 2, PeakMemoryBytes: 0},
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
