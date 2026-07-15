package contract

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Explanation struct {
	FormatVersion string    `json:"format_version"`
	Mode          string    `json:"mode"`
	Statement     Statement `json:"statement"`
	Timing        Timing    `json:"timing"`
	Plan          Operator  `json:"plan"`
}

type Timing struct {
	PlanningMS  float64  `json:"planning_ms"`
	ExecutionMS *float64 `json:"execution_ms,omitempty"`
}

type Statement struct {
	Kind     string `json:"kind"`
	ReadOnly bool   `json:"read_only"`
	SQL      string `json:"sql"`
}

type Operator struct {
	ID             int              `json:"id"`
	Operator       string           `json:"operator"`
	LogicalPurpose string           `json:"logical_purpose"`
	Object         string           `json:"object,omitempty"`
	Access         string           `json:"access,omitempty"`
	Index          string           `json:"index,omitempty"`
	Hints          []string         `json:"hints,omitempty"`
	Predicates     []Predicate      `json:"predicates,omitempty"`
	Estimate       Estimate         `json:"estimate"`
	Output         OutputProperties `json:"output"`
	Statistics     []Statistic      `json:"statistics,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
	Actual         *Actual          `json:"actual,omitempty"`
	Choice         *Choice          `json:"choice,omitempty"`
	Explanation    string           `json:"explanation"`
	Children       []Operator       `json:"children,omitempty"`
}

type Estimate struct {
	Rows        float64 `json:"rows"`
	RowWidthB   int     `json:"row_width_bytes"`
	Cost        float64 `json:"cost"`
	PeakMemoryB int     `json:"peak_memory_bytes"`
}

type Predicate struct {
	Kind       string `json:"kind"`
	Expression string `json:"expression"`
}

type OutputProperties struct {
	Ordering   []string   `json:"ordering,omitempty"`
	UniqueKeys [][]string `json:"unique_keys,omitempty"`
}

type Statistic struct {
	Object    string `json:"object"`
	Version   string `json:"version"`
	Collected string `json:"collected_at"`
	Stale     bool   `json:"stale"`
}

type Actual struct {
	Invocations       int         `json:"invocations"`
	InputRows         int         `json:"input_rows"`
	OutputRows        int         `json:"output_rows"`
	FilteredRows      int         `json:"filtered_rows"`
	FirstRowMS        float64     `json:"first_row_ms"`
	TotalMS           float64     `json:"total_ms"`
	PeakMemoryB       int         `json:"peak_memory_bytes"`
	SpillCount        int         `json:"spill_count"`
	SpillBytes        int         `json:"spill_bytes"`
	TemporaryStorageB int         `json:"temporary_storage_bytes"`
	Storage           StorageWork `json:"storage"`
	Wait              WaitTime    `json:"wait"`
	RowsVsEstimate    float64     `json:"rows_vs_estimate_ratio"`
	Warnings          []string    `json:"warnings,omitempty"`
}

type StorageWork struct {
	LogicalReads  int `json:"logical_reads"`
	PhysicalReads int `json:"physical_reads"`
	BytesRead     int `json:"bytes_read"`
}

type WaitTime struct {
	LockMS  float64 `json:"lock_ms"`
	OtherMS float64 `json:"other_ms"`
}

type Choice struct {
	Selected     string        `json:"selected"`
	Reason       string        `json:"reason"`
	Alternatives []Alternative `json:"alternatives"`
}

type Alternative struct {
	Name           string  `json:"name"`
	EstimatedCost  float64 `json:"estimated_cost"`
	RejectedReason string  `json:"rejected_reason"`
}

func Example(analyzed bool) Explanation {
	scan := Operator{
		ID:             2,
		Operator:       "index_range_scan",
		LogicalPurpose: "filter and order rows",
		Object:         "app.orders",
		Access:         "range",
		Index:          "orders_customer_created",
		Hints:          []string{"USE INDEX (orders_customer_created): applicable"},
		Predicates: []Predicate{
			{Kind: "access", Expression: "customer_id = ?"},
		},
		Estimate: Estimate{Rows: 47, RowWidthB: 24, Cost: 8.4, PeakMemoryB: 0},
		Output: OutputProperties{
			Ordering: []string{"created_at DESC"},
		},
		Statistics: []Statistic{
			{Object: "app.orders", Version: "stats-184", Collected: "2026-07-15T09:30:00Z", Stale: false},
		},
		Choice: &Choice{
			Selected: "orders_customer_created",
			Reason:   "matches customer_id and supplies created_at DESC order",
			Alternatives: []Alternative{
				{
					Name:           "table_scan_then_sort",
					EstimatedCost:  924.2,
					RejectedReason: "reads the whole table and requires a sort",
				},
			},
		},
		Explanation: "read this customer's newest orders directly from the composite index",
	}
	root := Operator{
		ID:             1,
		Operator:       "limit",
		LogicalPurpose: "limit result cardinality",
		Estimate:       Estimate{Rows: 20, RowWidthB: 24, Cost: 8.6, PeakMemoryB: 0},
		Output: OutputProperties{
			Ordering: []string{"created_at DESC"},
		},
		Explanation: "stop after 20 ordered rows",
		Children:    []Operator{scan},
	}
	mode := "plan"
	timing := Timing{PlanningMS: 0.31}
	if analyzed {
		mode = "analyze"
		executionMS := 0.74
		timing.ExecutionMS = &executionMS
		root.Actual = &Actual{
			Invocations:    1,
			InputRows:      20,
			OutputRows:     20,
			FirstRowMS:     0.18,
			TotalMS:        0.72,
			RowsVsEstimate: 1,
		}
		root.Children[0].Actual = &Actual{
			Invocations: 1,
			InputRows:   20,
			OutputRows:  20,
			FirstRowMS:  0.16,
			TotalMS:     0.68,
			PeakMemoryB: 4096,
			Storage: StorageWork{
				LogicalReads:  5,
				PhysicalReads: 1,
				BytesRead:     16384,
			},
			Wait:           WaitTime{LockMS: 0, OtherMS: 0.02},
			RowsVsEstimate: 0.43,
		}
	}
	return Explanation{
		FormatVersion: "1.0",
		Mode:          mode,
		Timing:        timing,
		Statement: Statement{
			Kind:     "select",
			ReadOnly: true,
			SQL:      "SELECT id, total FROM orders WHERE customer_id = ? ORDER BY created_at DESC LIMIT 20",
		},
		Plan: root,
	}
}

func RenderJSON(explanation Explanation) string {
	encoded, err := json.MarshalIndent(explanation, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(encoded)
}

func RenderTable(explanation Explanation) string {
	var rows []string
	rows = append(rows, "MYSQL PREFIX")
	rows = append(rows, "id  select_type  table   partitions  type   possible_keys             key                       key_len  ref    rows  filtered  Extra")
	rows = append(rows, "--  -----------  ------  ----------  -----  ------------------------  ------------------------  -------  -----  ----  --------  ----------------")
	rows = append(rows, "1   SIMPLE       NULL    NULL        NULL   NULL                      NULL                      NULL     NULL   20    100.00    Limit: 20")
	rows = append(rows, "1   SIMPLE       orders  NULL        range  orders_customer_created   orders_customer_created   8        const  47    100.00    Backward index scan")
	rows = append(rows, "")
	rows = append(rows, "STABLE EXTENSIONS")
	rows = append(rows, "operator_id  parent_operator_id  operator          cost  memory  actual_rows  loops  first_ms  total_ms  reason")
	rows = append(rows, "-----------  ------------------  ----------------  ----  ------  -----------  -----  --------  --------  ----------------------------------------")
	flatten(&rows, explanation.Plan, 0)
	rows = append(rows, "warnings: NULL")
	rows = append(rows, "JSON-only detail: structured predicates, provenance, output properties, and rejected alternatives.")
	return strings.Join(rows, "\n")
}

func flatten(rows *[]string, operator Operator, parent int) {
	actualRows := "-"
	totalMS := "-"
	if operator.Actual != nil {
		actualRows = fmt.Sprintf("%d", operator.Actual.OutputRows)
		totalMS = fmt.Sprintf("%.2fms", operator.Actual.TotalMS)
	}
	parentText := "NULL"
	if parent != 0 {
		parentText = fmt.Sprintf("%d", parent)
	}
	*rows = append(*rows, fmt.Sprintf(
		"%-11d  %-18s  %-16s  %-4.1f  %-6d  %-11s  %-5s  %-8s  %-8s  %s",
		operator.ID,
		parentText,
		operator.Operator,
		operator.Estimate.Cost,
		operator.Estimate.PeakMemoryB,
		actualRows,
		actualLoops(operator),
		actualFirstRow(operator),
		totalMS,
		operator.Explanation,
	))
	for _, child := range operator.Children {
		flatten(rows, child, operator.ID)
	}
}

func actualLoops(operator Operator) string {
	if operator.Actual == nil {
		return "NULL"
	}
	return fmt.Sprintf("%d", operator.Actual.Invocations)
}

func actualFirstRow(operator Operator) string {
	if operator.Actual == nil {
		return "NULL"
	}
	return fmt.Sprintf("%.2f", operator.Actual.FirstRowMS)
}
