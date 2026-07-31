package queryexplanation

import (
	"sync"
	"time"
)

// RuntimeMetrics stores counters observed by the executor. It is safe to read
// while a statement is running, so it is also the source for a live snapshot.
// Each record names the physical operator that produced the evidence.
type RuntimeMetrics struct {
	mu          sync.RWMutex
	operatorIDs map[string][]int
	seen        map[string]int
	operators   map[int]Actual
	rootID      int
}

// NewRuntimeMetrics prepares a collector for one immutable explanation plan.
func NewRuntimeMetrics(plan *Document) *RuntimeMetrics {
	metrics := &RuntimeMetrics{
		operatorIDs: make(map[string][]int),
		seen:        make(map[string]int),
		operators:   make(map[int]Actual),
	}
	metrics.rootID = collectOperatorIDs(plan, metrics.operatorIDs)
	return metrics
}

func collectOperatorIDs(document *Document, operatorIDs map[string][]int) int {
	if document == nil {
		return 0
	}
	var visit func(*Operator)
	visit = func(operator *Operator) {
		if operator == nil {
			return
		}
		operatorIDs[operator.Kind] = append(operatorIDs[operator.Kind], operator.ID)
		for _, child := range operator.Children {
			visit(child)
		}
	}
	visit(document.Plan)
	if document.Plan == nil {
		return 0
	}
	return document.Plan.ID
}

// Record adds observed evidence to the next physical operator of kind. The
// executor calls it once for each measured pipeline stage.
func (m *RuntimeMetrics) Record(kind string, inputRows, outputRows, filteredRows, logicalReads, bytesRead, peakMemoryBytes int, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.operatorIDs[kind]
	index := m.seen[kind]
	if index >= len(ids) {
		return
	}
	m.seen[kind] = index + 1
	id := ids[index]
	actual := m.operators[id]
	actual.Invocations++
	actual.InputRows += inputRows
	actual.OutputRows += outputRows
	actual.FilteredRows += filteredRows
	actual.Storage.LogicalReads += logicalReads
	actual.Storage.BytesRead += bytesRead
	if peakMemoryBytes > actual.PeakMemoryBytes {
		actual.PeakMemoryBytes = peakMemoryBytes
	}
	total := valueOrZero(actual.TotalMS) + milliseconds(elapsed)
	actual.TotalMS = &total
	m.operators[id] = actual
}

// SetRoot records statement-level evidence that belongs to the root physical
// operator, such as a lock wait observed before the statement completes.
func (m *RuntimeMetrics) SetRoot(outputRows, peakMemoryBytes int, elapsed, lockWait time.Duration, complete bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rootID == 0 {
		return
	}
	actual := m.operators[m.rootID]
	if actual.Invocations == 0 {
		actual.Invocations = 1
	}
	actual.OutputRows = outputRows
	if peakMemoryBytes > actual.PeakMemoryBytes {
		actual.PeakMemoryBytes = peakMemoryBytes
	}
	total := milliseconds(elapsed)
	actual.TotalMS = &total
	if complete {
		first := total
		actual.FirstRowMS = &first
	}
	actual.Wait.LockMS = milliseconds(lockWait)
	m.operators[m.rootID] = actual
}

func (m *RuntimeMetrics) copyOperators() map[int]Actual {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[int]Actual, len(m.operators))
	for id, actual := range m.operators {
		result[id] = cloneActual(actual)
	}
	return result
}

// Analyze returns the completed runtime form of a plan. The result count is
// measured from the executed result stream; execution time covers the same
// run. The returned document never mutates the plan that supplied it.
func Analyze(plan *Document, elapsed time.Duration, resultRows int) *Document {
	metrics := NewRuntimeMetrics(plan)
	metrics.SetRoot(resultRows, 0, elapsed, 0, true)
	return AnalyzeWithMetrics(plan, elapsed, metrics)
}

// AnalyzeWithMetrics returns completed runtime evidence from one executed
// statement. It never fills an operator from a different operator's counters.
func AnalyzeWithMetrics(plan *Document, elapsed time.Duration, metrics *RuntimeMetrics) *Document {
	document := cloneRuntimeDocument(plan)
	document.Mode = "analyze"
	document.Partial = false
	document.Snapshot = nil
	document.Timing.Execution = &ExecutionTiming{ElapsedMS: milliseconds(elapsed), Complete: true}
	attachCompletedRuntime(document.Plan, metrics.copyOperators())
	return document
}

// Snapshot returns the partial runtime form of a plan that is still running.
// It records only evidence visible when the caller captured the snapshot.
func Snapshot(plan *Document, connectionID uint32, capturedAt time.Time, elapsed time.Duration) *Document {
	return SnapshotWithMetrics(plan, connectionID, capturedAt, elapsed, 0, nil)
}

// SnapshotWithMetrics returns only counters observed through capturedAt. The
// root always has elapsed statement evidence; other operators appear only when
// the executor recorded them.
func SnapshotWithMetrics(plan *Document, connectionID uint32, capturedAt time.Time, elapsed, lockWait time.Duration, metrics *RuntimeMetrics) *Document {
	document := cloneRuntimeDocument(plan)
	document.Mode = "snapshot"
	document.Partial = true
	document.Timing.Execution = &ExecutionTiming{ElapsedMS: milliseconds(elapsed), Complete: false}
	document.Warnings = append(document.Warnings, partialSnapshotWarning())
	document.Snapshot = &SnapshotDetails{ConnectionID: connectionID, CapturedAt: capturedAt.UTC().Format(time.RFC3339Nano)}
	if metrics == nil {
		metrics = NewRuntimeMetrics(plan)
	}
	observed := metrics.copyOperators()
	setRoot(observed, metrics.rootID, 0, 0, elapsed, lockWait, false)
	attachPartialRuntime(document.Plan, observed)
	return document
}

func setRoot(operators map[int]Actual, rootID, outputRows, peakMemoryBytes int, elapsed, lockWait time.Duration, complete bool) {
	if rootID == 0 {
		return
	}
	actual := operators[rootID]
	if actual.Invocations == 0 {
		actual.Invocations = 1
	}
	actual.OutputRows = outputRows
	if peakMemoryBytes > actual.PeakMemoryBytes {
		actual.PeakMemoryBytes = peakMemoryBytes
	}
	total := milliseconds(elapsed)
	actual.TotalMS = &total
	if complete {
		first := total
		actual.FirstRowMS = &first
	}
	actual.Wait.LockMS = milliseconds(lockWait)
	operators[rootID] = actual
}

func attachCompletedRuntime(operator *Operator, observed map[int]Actual) {
	if operator == nil {
		return
	}
	actual, found := observed[operator.ID]
	if !found {
		actual = unavailableActual()
	}
	actual.RowsVsEstimateRatio = estimateRatio(actual.OutputRows, operator.Estimates.Rows)
	operator.Actual = &actual
	for _, child := range operator.Children {
		attachCompletedRuntime(child, observed)
	}
}

func attachPartialRuntime(operator *Operator, observed map[int]Actual) {
	if operator == nil {
		return
	}
	if actual, found := observed[operator.ID]; found {
		actual.RowsVsEstimateRatio = nil
		actual.Warnings = append(actual.Warnings, Warning{
			Code: "PARTIAL_RUNTIME_EVIDENCE", Severity: "info",
			Summary: "Runtime counters were captured before this operator completed.",
		})
		operator.Actual = &actual
	}
	for _, child := range operator.Children {
		attachPartialRuntime(child, observed)
	}
}

func unavailableActual() Actual {
	return Actual{Warnings: []Warning{{
		Code: "RUNTIME_OPERATOR_NOT_INVOKED", Severity: "info",
		Summary: "The operator did not run during this execution.",
	}}}
}

func cloneActual(source Actual) Actual {
	result := source
	if source.FirstRowMS != nil {
		first := *source.FirstRowMS
		result.FirstRowMS = &first
	}
	if source.TotalMS != nil {
		total := *source.TotalMS
		result.TotalMS = &total
	}
	result.Warnings = append([]Warning(nil), source.Warnings...)
	return result
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
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
