// Package queryexplanation builds the public v0.1 query-explanation contract.
// It produces the canonical format-1 JSON document and its stable
// MySQL-oriented tabular projection from a planned statement. It owns the
// public explanation surface only; it neither parses SQL nor executes queries.
package queryexplanation

// FormatVersion is the fixed major version of the canonical JSON contract.
const FormatVersion = 1

// FixedSQLMode is the fixed v0.1 SQL mode reported in planning settings.
const FixedSQLMode = "STRICT_ALL_TABLES,ONLY_FULL_GROUP_BY,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO"

const rowWidthPerColumn = 16

// Document is the canonical explanation envelope.
type Document struct {
	FormatVersion int              `json:"format_version"`
	ServerVersion string           `json:"server_version"`
	Mode          string           `json:"mode"`
	Partial       bool             `json:"partial"`
	Statement     Statement        `json:"statement"`
	Timing        Timing           `json:"timing"`
	Plan          *Operator        `json:"plan"`
	Warnings      []Warning        `json:"warnings"`
	Snapshot      *SnapshotDetails `json:"snapshot,omitempty"`
}

// Statement records the submitted statement and its public planning context.
type Statement struct {
	Kind             string            `json:"kind"`
	SQL              string            `json:"sql"`
	ReadOnly         bool              `json:"read_only"`
	LockingRead      bool              `json:"locking_read"`
	CurrentDatabase  string            `json:"current_database,omitempty"`
	Parameters       []Parameter       `json:"parameters"`
	PlanningSettings map[string]string `json:"planning_settings"`
}

// Parameter exposes a prepared parameter's position and type only. Bound
// values are never represented.
type Parameter struct {
	Position int    `json:"position"`
	Type     string `json:"type"`
}

// Timing reports planning time. Plan-only documents omit execution timing.
type Timing struct {
	PlanningMS float64          `json:"planning_ms"`
	Execution  *ExecutionTiming `json:"execution,omitempty"`
}

// ExecutionTiming records the observed duration of an executed explanation.
type ExecutionTiming struct {
	ElapsedMS float64 `json:"elapsed_ms"`
	Complete  bool    `json:"complete"`
}

// SnapshotDetails identifies the active session and the capture instant for a
// partial live explanation.
type SnapshotDetails struct {
	ConnectionID uint32 `json:"connection_id"`
	CapturedAt   string `json:"captured_at"`
}

// OperatorIdentity holds the public tree identifier and its private executor
// identity. Keeping them together prevents runtime bookkeeping from widening
// the public operator model.
type OperatorIdentity struct {
	ID int `json:"id"`
	// RuntimeKey is an executor-only identity. It is deliberately excluded from
	// the public document so runtime evidence can be joined to this exact
	// physical operator without exposing internal bookkeeping.
	RuntimeKey string `json:"-"`
}

// Operator is one physical operator in the explanation tree.
type Operator struct {
	OperatorIdentity
	Kind          string            `json:"kind"`
	Summary       string            `json:"summary"`
	Operation     any               `json:"operation"`
	Strategy      *Strategy         `json:"strategy,omitempty"`
	Objects       []ObjectReference `json:"objects,omitempty"`
	Predicates    []Predicate       `json:"predicates,omitempty"`
	Choice        *Choice           `json:"choice,omitempty"`
	Statistics    []Statistic       `json:"statistics,omitempty"`
	Opportunities []Opportunity     `json:"opportunities,omitempty"`
	Estimates     Estimates         `json:"estimates"`
	Output        Output            `json:"output"`
	Warnings      []Warning         `json:"warnings"`
	Children      []*Operator       `json:"children"`
	Actual        *Actual           `json:"actual,omitempty"`
}

// Actual is the observed runtime evidence for one operator. Nil timing fields
// are encoded as JSON null when the value did not become available.
type Actual struct {
	Invocations           int             `json:"invocations"`
	InputRows             int             `json:"input_rows"`
	OutputRows            int             `json:"output_rows"`
	FilteredRows          int             `json:"filtered_rows"`
	FirstRowMS            *float64        `json:"first_row_ms"`
	TotalMS               *float64        `json:"total_ms"`
	PeakMemoryBytes       int             `json:"peak_memory_bytes"`
	SpillCount            int             `json:"spill_count"`
	SpillBytes            int             `json:"spill_bytes"`
	TemporaryStorageBytes int             `json:"temporary_storage_bytes"`
	Storage               StorageEvidence `json:"storage"`
	Wait                  WaitEvidence    `json:"wait"`
	RowsVsEstimateRatio   *float64        `json:"rows_vs_estimate_ratio"`
	Warnings              []Warning       `json:"warnings"`
}

// StorageEvidence records storage reads that the statement observed.
type StorageEvidence struct {
	LogicalReads  int `json:"logical_reads"`
	PhysicalReads int `json:"physical_reads"`
	BytesRead     int `json:"bytes_read"`
}

// WaitEvidence records the wait time that the statement observed.
type WaitEvidence struct {
	LockMS  float64 `json:"lock_ms"`
	OtherMS float64 `json:"other_ms"`
}

// Strategy names a documented execution tactic within an operator kind.
type Strategy struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// ObjectReference identifies a schema object an operator touches.
type ObjectReference struct {
	Type     string `json:"type"`
	Database string `json:"database,omitempty"`
	Table    string `json:"table,omitempty"`
	Name     string `json:"name"`
}

// Predicate is a canonical predicate with its originating SQL construct.
type Predicate struct {
	Role       string            `json:"role"`
	Expression string            `json:"expression"`
	Sources    []PredicateSource `json:"sources"`
}

// PredicateSource is the SQL clause a predicate originated from.
type PredicateSource struct {
	Clause   string `json:"clause"`
	Fragment string `json:"fragment"`
}

// CodeSummary identifies a stable planner reason and its user-facing meaning.
type CodeSummary struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

// Choice records the selected access path and the credible alternatives.
type Choice struct {
	Selected     string        `json:"selected"`
	Reason       CodeSummary   `json:"reason"`
	Alternatives []Alternative `json:"alternatives"`
}

// Alternative records one rejected plan shape.
type Alternative struct {
	Name          string      `json:"name"`
	EstimatedCost float64     `json:"estimated_cost"`
	Reason        CodeSummary `json:"reason"`
}

// Statistic records catalog evidence used while a plan is built.
type Statistic struct {
	Object       ObjectReference `json:"object"`
	Kind         string          `json:"kind"`
	CollectedAt  string          `json:"collected_at"`
	ObservedRows int             `json:"observed_rows"`
	Stale        bool            `json:"stale"`
	Limitations  []string        `json:"limitations"`
}

// Opportunity records a stable, non-prescriptive planning opportunity.
type Opportunity struct {
	Code     string   `json:"code"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
}

// Estimates are the planner's pre-execution estimates for an operator.
type Estimates struct {
	Rows            float64 `json:"rows"`
	RowWidthBytes   int     `json:"row_width_bytes"`
	Cost            float64 `json:"cost"`
	PeakMemoryBytes int     `json:"peak_memory_bytes"`
}

// Output describes an operator's projected columns, ordering, and unique keys.
type Output struct {
	Columns    []string       `json:"columns"`
	Ordering   []OrderingTerm `json:"ordering"`
	UniqueKeys [][]string     `json:"unique_keys"`
}

// OrderingTerm is one guaranteed ordering expression and direction.
type OrderingTerm struct {
	Expression string `json:"expression"`
	Direction  string `json:"direction"`
}

// Warning is a structured, code-identified explanation warning.
type Warning struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

// scanOperation records the physical source and traversal direction.
type scanOperation struct {
	Source    string `json:"source"`
	Direction string `json:"direction"`
}

// filterOperation records which clause a filter enforces.
type filterOperation struct {
	Role string `json:"role"`
}

// projectOperation records the number of projected expressions.
type projectOperation struct {
	ExpressionCount int `json:"expression_count"`
}

// valuesOperation records the number of literal rows produced.
type valuesOperation struct {
	RowCount int `json:"row_count"`
}

// mutationOperation records the kind of durable write performed.
type mutationOperation struct {
	MutationType string `json:"mutation_type"`
}

type joinOperation struct {
	JoinType string `json:"join_type"`
}

type sortOperation struct {
	Purpose string `json:"purpose"`
}

type limitOperation struct {
	OffsetPresent bool `json:"offset_present"`
}

type distinctOperation struct {
	Scope string `json:"scope"`
}

type setOperation struct {
	SetOperation string `json:"set_operation"`
	All          bool   `json:"all"`
}

type aggregateOperation struct {
	Scope               string   `json:"scope"`
	AggregateCount      int      `json:"aggregate_count"`
	GroupingExpressions []string `json:"grouping_expressions"`
}

type windowOperation struct {
	WindowCount   int      `json:"window_count"`
	FunctionCount int      `json:"function_count"`
	Windows       []Window `json:"windows"`
}

type materializeOperation struct {
	Reason string `json:"reason"`
}

type constraintCheckOperation struct {
	ConstraintType string `json:"constraint_type"`
	ConstraintName string `json:"constraint_name"`
}

type lockOperation struct {
	Mode       string `json:"mode"`
	WaitPolicy string `json:"wait_policy"`
}

// Table is the neutral relation description a planner needs.
type Table struct {
	Database string
	Name     string
	Columns  []string
	RowCount int
	Access   *IndexAccess
}

// IndexAccess describes the selected B-tree path for a relation.
type IndexAccess struct {
	Name        string
	Unique      bool
	Forced      bool
	Covering    bool
	CollectedAt string
}

// Constraint is a public constraint check in a write plan.
type Constraint struct {
	Type  string
	Name  string
	Table Table
}

func emptyOutput() Output {
	return Output{Columns: []string{}, Ordering: []OrderingTerm{}, UniqueKeys: [][]string{}}
}

func columnOutput(table Table, columns []string) Output {
	qualified := make([]string, len(columns))
	for index, column := range columns {
		qualified[index] = table.Name + "." + column
	}
	return Output{Columns: qualified, Ordering: []OrderingTerm{}, UniqueKeys: [][]string{}}
}

func rowWidth(columnCount int) int {
	return columnCount * rowWidthPerColumn
}
