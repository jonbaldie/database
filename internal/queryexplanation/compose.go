package queryexplanation

// PlanOperator returns a plan-only document around a composed operator tree.
func PlanOperator(serverVersion, sql, currentDatabase string, root *Operator) *Document {
	statement := Statement{Kind: "select", SQL: sql, ReadOnly: true, LockingRead: false}
	return newDocument(serverVersion, currentDatabase, statement, root)
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
func MaterializedInput(primary, input *Operator, reason, clause, fragment string) *Operator {
	predicates := []Predicate{}
	if clause != "" && fragment != "" {
		predicates = append(predicates, Predicate{
			Role: "residual", Expression: fragment,
			Sources: []PredicateSource{{Clause: clause, Fragment: fragment}},
		})
	}
	return &Operator{
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
}

// ReusedInput records a read from an earlier statement-scoped materialization.
func ReusedInput(primary *Operator, columns []string) *Operator {
	reused := &Operator{
		Kind: "materialize", Summary: "Read the statement-scoped materialized input.",
		Operation: materializeOperation{Reason: "cte"}, Estimates: Estimates{},
		Output:   Output{Columns: append([]string(nil), columns...), Ordering: []OrderingTerm{}, UniqueKeys: [][]string{}},
		Warnings: []Warning{}, Children: []*Operator{},
	}
	return &Operator{
		Kind: "materialize", Summary: "Reuse the previously materialized composed query input.",
		Operation: materializeOperation{Reason: "reuse"},
		Estimates: primary.Estimates, Output: primary.Output, Warnings: []Warning{}, Children: []*Operator{reused, primary},
	}
}

// DependentInput records a correlated subquery evaluated for each outer row.
func DependentInput(primary, input *Operator, clause, fragment string) *Operator {
	predicates := []Predicate{{
		Role: "join", Expression: fragment,
		Sources: []PredicateSource{{Clause: clause, Fragment: fragment}},
	}}
	return &Operator{
		Kind: "join", Summary: "Evaluate the correlated subquery for each outer row.",
		Operation: joinOperation{JoinType: "left"}, Predicates: predicates,
		Estimates: Estimates{
			Rows: primary.Estimates.Rows, RowWidthBytes: primary.Estimates.RowWidthBytes,
			Cost: primary.Estimates.Cost + primary.Estimates.Rows*input.Estimates.Cost, PeakMemoryBytes: input.Estimates.RowWidthBytes * int(input.Estimates.Rows),
		},
		Output: primary.Output, Warnings: []Warning{}, Children: []*Operator{primary, input},
	}
}

// OrderedInput adds the global ORDER BY of a set expression.
func OrderedInput(input *Operator, orders []Order) *Operator { return sortOperator(orders, input) }

// LimitedInput adds the global LIMIT of a set expression.
func LimitedInput(input *Operator, limit Limit) *Operator { return limitOperator(limit, input) }
