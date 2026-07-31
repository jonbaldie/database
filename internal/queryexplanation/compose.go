package queryexplanation

// PlanOperator returns a plan-only document around a composed operator tree.
func PlanOperator(serverVersion, sql, currentDatabase string, root *Operator) *Document {
	lockingRead := containsLock(root)
	statement := Statement{Kind: "select", SQL: sql, ReadOnly: !lockingRead, LockingRead: lockingRead}
	return newDocument(serverVersion, currentDatabase, statement, root)
}

func containsLock(operator *Operator) bool {
	if operator == nil {
		return false
	}
	if operator.Kind == "lock" {
		return true
	}
	for _, child := range operator.Children {
		if containsLock(child) {
			return true
		}
	}
	return false
}

// SetOperation combines two SELECT inputs with one SQL set operation.
func SetOperation(operation string, all bool, left, right *Operator) *Operator {
	rows := left.Estimates.Rows + right.Estimates.Rows
	output := Output{Columns: append([]string(nil), left.Output.Columns...), Ordering: []OrderingTerm{}, UniqueKeys: [][]string{}}
	return &Operator{
		Kind:      "set_operation",
		Summary:   "Combine the input rows with the requested SQL set operation.",
		Operation: setOperation{SetOperation: operation, All: all},
		Estimates: Estimates{Rows: rows, RowWidthBytes: left.Estimates.RowWidthBytes, Cost: left.Estimates.Cost + right.Estimates.Cost + rows, PeakMemoryBytes: left.Estimates.RowWidthBytes * int(rows)},
		Output:    output,
		Warnings:  []Warning{},
		Children:  []*Operator{left, right},
	}
}

// MaterializedInput records a composed SELECT input and the SQL construct that
// introduced it. The materialized input executes before its consumer.
func MaterializedInput(primary, input *Operator, reason, clause, fragment string, runtimeKeys ...string) *Operator {
	predicates := []Predicate{}
	if clause != "" && fragment != "" {
		predicates = append(predicates, Predicate{
			Role: "residual", Expression: fragment,
			Sources: []PredicateSource{{Clause: clause, Fragment: fragment}},
		})
	}
	operator := &Operator{
		Kind:       "materialize",
		Summary:    "Materialize the composed query input for this statement.",
		Operation:  materializeOperation{Reason: reason},
		Predicates: predicates,
		Estimates: Estimates{
			Rows: primary.Estimates.Rows, RowWidthBytes: primary.Estimates.RowWidthBytes,
			Cost: primary.Estimates.Cost + input.Estimates.Cost, PeakMemoryBytes: input.Estimates.RowWidthBytes * int(input.Estimates.Rows),
		},
		Output: primary.Output, Warnings: []Warning{}, Children: []*Operator{input, primary},
	}
	operator.RuntimeKey = firstRuntimeKey(runtimeKeys)
	return operator
}

// AdditionalMaterializedInput appends another pre-consumer materialization to
// an existing composed-input sequence without reversing source order.
func AdditionalMaterializedInput(primary, input *Operator, reason, clause, fragment string, runtimeKeys ...string) *Operator {
	predicates := []Predicate{}
	if clause != "" && fragment != "" {
		predicates = append(predicates, Predicate{
			Role: "residual", Expression: fragment,
			Sources: []PredicateSource{{Clause: clause, Fragment: fragment}},
		})
	}
	materialized := &Operator{
		Kind: "materialize", Summary: "Materialize the composed query input for this statement.",
		Operation: materializeOperation{Reason: reason}, Predicates: predicates,
		Estimates: input.Estimates, Output: input.Output, Warnings: []Warning{}, Children: []*Operator{input},
	}
	materialized.RuntimeKey = firstRuntimeKey(runtimeKeys)
	insertExecutionInput(primary, materialized)
	return primary
}

// ReusedInput records a read from an earlier statement-scoped materialization.
func ReusedInput(primary *Operator, columns []string, runtimeKeys ...string) *Operator {
	reused := &Operator{
		Kind: "materialize", Summary: "Read the statement-scoped materialized input.",
		Operation: materializeOperation{Reason: "reuse"}, Estimates: Estimates{},
		Output:   Output{Columns: append([]string(nil), columns...), Ordering: []OrderingTerm{}, UniqueKeys: [][]string{}},
		Warnings: []Warning{}, Children: []*Operator{},
	}
	reused.RuntimeKey = firstRuntimeKey(runtimeKeys)
	insertExecutionInput(primary, reused)
	return primary
}

func firstRuntimeKey(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func insertExecutionInput(primary, input *Operator) {
	insert := len(primary.Children)
	if insert > 0 {
		insert--
	}
	primary.Children = append(primary.Children, nil)
	copy(primary.Children[insert+1:], primary.Children[insert:])
	primary.Children[insert] = input
}

// DependentInput records a correlated subquery evaluated for each outer row.
func DependentInput(primary, input *Operator, clause, fragment string, runtimeKeys ...string) *Operator {
	predicates := []Predicate{{
		Role: "join", Expression: fragment,
		Sources: []PredicateSource{{Clause: clause, Fragment: fragment}},
	}}
	operator := &Operator{
		Kind: "join", Summary: "Evaluate the correlated subquery for each outer row.",
		Operation: joinOperation{JoinType: "left"}, Predicates: predicates,
		Estimates: Estimates{
			Rows: primary.Estimates.Rows, RowWidthBytes: primary.Estimates.RowWidthBytes,
			Cost: primary.Estimates.Cost + primary.Estimates.Rows*input.Estimates.Cost, PeakMemoryBytes: input.Estimates.RowWidthBytes * int(input.Estimates.Rows),
		},
		Output: primary.Output, Warnings: []Warning{}, Children: []*Operator{primary, input},
	}
	operator.RuntimeKey = firstRuntimeKey(runtimeKeys)
	return operator
}

// OrderedInput adds the global ORDER BY of a set expression.
func OrderedInput(input *Operator, orders []Order) *Operator { return sortOperator(orders, input) }

// LimitedInput adds the global LIMIT of a set expression.
func LimitedInput(input *Operator, limit Limit) *Operator { return limitOperator(limit, input) }
