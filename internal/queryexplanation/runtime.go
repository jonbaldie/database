package queryexplanation

import "time"

// Analyze returns the completed runtime form of a plan. The result count is
// measured from the executed result stream; execution time covers the same
// run. The returned document never mutates the plan that supplied it.
func Analyze(plan *Document, elapsed time.Duration, resultRows int) *Document {
	document := cloneRuntimeDocument(plan)
	document.Mode = "analyze"
	document.Partial = false
	document.Snapshot = nil
	document.Timing.Execution = &ExecutionTiming{ElapsedMS: milliseconds(elapsed), Complete: true}
	attachRuntime(document.Plan, elapsed, resultRows, false)
	return document
}

// Snapshot returns the partial runtime form of a plan that is still running.
// It records only evidence visible when the caller captured the snapshot.
func Snapshot(plan *Document, connectionID uint32, capturedAt time.Time, elapsed time.Duration) *Document {
	document := cloneRuntimeDocument(plan)
	document.Mode = "snapshot"
	document.Partial = true
	document.Timing.Execution = &ExecutionTiming{ElapsedMS: milliseconds(elapsed), Complete: false}
	document.Warnings = append(document.Warnings, partialSnapshotWarning())
	document.Snapshot = &SnapshotDetails{ConnectionID: connectionID, CapturedAt: capturedAt.UTC().Format(time.RFC3339Nano)}
	attachRuntime(document.Plan, elapsed, 0, true)
	return document
}

func attachRuntime(operator *Operator, elapsed time.Duration, resultRows int, partial bool) {
	if operator == nil {
		return
	}
	total := milliseconds(elapsed)
	actual := Actual{
		Invocations:           1,
		InputRows:             resultRows,
		OutputRows:            resultRows,
		FilteredRows:          0,
		FirstRowMS:            nil,
		TotalMS:               &total,
		PeakMemoryBytes:       0,
		SpillCount:            0,
		SpillBytes:            0,
		TemporaryStorageBytes: 0,
		Storage:               StorageEvidence{},
		Wait:                  WaitEvidence{},
		RowsVsEstimateRatio:   estimateRatio(resultRows, operator.Estimates.Rows),
		Warnings:              []Warning{},
	}
	if partial {
		actual.RowsVsEstimateRatio = nil
		actual.Warnings = append(actual.Warnings, Warning{
			Code: "PARTIAL_RUNTIME_EVIDENCE", Severity: "info",
			Summary: "Runtime counters were captured before this operator completed.",
		})
	}
	operator.Actual = &actual
	for _, child := range operator.Children {
		attachRuntime(child, elapsed, resultRows, partial)
	}
}

func estimateRatio(rows int, estimate float64) *float64 {
	if estimate <= 0 {
		return nil
	}
	ratio := float64(rows) / estimate
	return &ratio
}

func partialSnapshotWarning() Warning {
	return Warning{
		Code: "PARTIAL_SNAPSHOT", Severity: "info",
		Summary: "Counters describe work observed through the capture time and are not final totals.",
	}
}

func milliseconds(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(duration) / float64(time.Millisecond)
}

func cloneRuntimeDocument(source *Document) *Document {
	if source == nil {
		return nil
	}
	document := *source
	document.Statement.Parameters = append([]Parameter(nil), source.Statement.Parameters...)
	document.Statement.PlanningSettings = cloneSettings(source.Statement.PlanningSettings)
	document.Warnings = append([]Warning(nil), source.Warnings...)
	document.Plan = cloneOperator(source.Plan)
	if source.Timing.Execution != nil {
		execution := *source.Timing.Execution
		document.Timing.Execution = &execution
	}
	if source.Snapshot != nil {
		snapshot := *source.Snapshot
		document.Snapshot = &snapshot
	}
	return &document
}

func cloneSettings(settings map[string]string) map[string]string {
	clone := make(map[string]string, len(settings))
	for name, value := range settings {
		clone[name] = value
	}
	return clone
}

func cloneOperator(source *Operator) *Operator {
	if source == nil {
		return nil
	}
	operator := *source
	operator.Objects = append([]ObjectReference(nil), source.Objects...)
	operator.Predicates = clonePredicates(source.Predicates)
	operator.Statistics = cloneStatistics(source.Statistics)
	operator.Opportunities = cloneOpportunities(source.Opportunities)
	operator.Output = cloneOutput(source.Output)
	operator.Warnings = append([]Warning(nil), source.Warnings...)
	if source.Strategy != nil {
		strategy := *source.Strategy
		operator.Strategy = &strategy
	}
	if source.Choice != nil {
		choice := *source.Choice
		choice.Alternatives = append([]Alternative(nil), source.Choice.Alternatives...)
		operator.Choice = &choice
	}
	if source.Actual != nil {
		actual := *source.Actual
		actual.Warnings = append([]Warning(nil), source.Actual.Warnings...)
		operator.Actual = &actual
	}
	operator.Children = make([]*Operator, len(source.Children))
	for index, child := range source.Children {
		operator.Children[index] = cloneOperator(child)
	}
	return &operator
}

func clonePredicates(source []Predicate) []Predicate {
	clone := make([]Predicate, len(source))
	for index, predicate := range source {
		clone[index] = predicate
		clone[index].Sources = append([]PredicateSource(nil), predicate.Sources...)
	}
	return clone
}

func cloneStatistics(source []Statistic) []Statistic {
	clone := append([]Statistic(nil), source...)
	for index := range clone {
		clone[index].Limitations = append([]string(nil), clone[index].Limitations...)
	}
	return clone
}

func cloneOpportunities(source []Opportunity) []Opportunity {
	clone := append([]Opportunity(nil), source...)
	for index := range clone {
		clone[index].Evidence = append([]string(nil), clone[index].Evidence...)
	}
	return clone
}

func cloneOutput(source Output) Output {
	output := source
	output.Columns = append([]string(nil), source.Columns...)
	output.Ordering = append([]OrderingTerm(nil), source.Ordering...)
	output.UniqueKeys = make([][]string, len(source.UniqueKeys))
	for index, key := range source.UniqueKeys {
		output.UniqueKeys[index] = append([]string(nil), key...)
	}
	return output
}
