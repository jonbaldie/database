package queryexplanation

import (
	"testing"
	"time"
)

func TestAnalyzeAddsCompleteRuntimeEvidence(t *testing.T) {
	plan := PlanSelect("0.1.0", "SELECT id FROM orders", "app", Select{Table: ordersTable(), Columns: []string{"id"}})
	document := Analyze(plan, 2*time.Millisecond, 3)
	decoded := decode(t, document)

	if decoded["mode"] != "analyze" || decoded["partial"] != false {
		t.Fatalf("analysis envelope = %#v", decoded)
	}
	execution := decoded["timing"].(map[string]any)["execution"].(map[string]any)
	if execution["complete"] != true {
		t.Fatalf("analysis timing = %#v", execution)
	}
	actual := decoded["plan"].(map[string]any)["actual"].(map[string]any)
	if actual["output_rows"] != float64(3) || actual["total_ms"] == nil {
		t.Fatalf("analysis runtime evidence = %#v", actual)
	}
	tabular := RenderTabular(document)
	actualRows := 18 // stable actual_rows column position
	if tabular.Null[0][actualRows] || tabular.Rows[0][actualRows] != "3" {
		t.Fatalf("analysis tabular rows = %#v/%#v", tabular.Rows[0], tabular.Null[0])
	}
}

func TestTabularRuntimeProjectionIncludesAllRequiredEvidence(t *testing.T) {
	plan := PlanSelect("0.1.0", "SELECT id FROM orders", "app", Select{Table: ordersTable(), Columns: []string{"id"}})
	metrics := NewRuntimeMetrics(plan)
	metrics.RecordOperator(metrics.OperatorIDs("scan")[0], 0, 3, 0, 3, 24, 0, time.Millisecond)
	metrics.RecordOperator(metrics.OperatorIDs("project")[0], 3, 3, 0, 0, 0, 72, time.Millisecond)
	metrics.SetRoot(3, 72, 2*time.Millisecond, 0, true)
	tabular := RenderTabular(AnalyzeWithMetrics(plan, 2*time.Millisecond, metrics))

	for _, required := range []string{
		"actual_rows", "total_ms", "actual_peak_memory_bytes", "actual_logical_reads",
		"actual_physical_reads", "actual_bytes_read", "actual_lock_ms", "actual_other_ms",
		"actual_rows_vs_estimate_ratio", "actual_warnings",
	} {
		found := false
		for _, column := range tabular.Columns {
			found = found || column == required
		}
		if !found {
			t.Errorf("tabular projection is missing %q", required)
		}
	}
}

func TestSnapshotAddsPartialRuntimeEvidence(t *testing.T) {
	plan := PlanSelect("0.1.0", "SELECT id FROM orders", "app", Select{Table: ordersTable(), Columns: []string{"id"}})
	document := Snapshot(plan, 42, time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC), 5*time.Millisecond)
	decoded := decode(t, document)

	if decoded["mode"] != "snapshot" || decoded["partial"] != true {
		t.Fatalf("snapshot envelope = %#v", decoded)
	}
	snapshot := decoded["snapshot"].(map[string]any)
	if snapshot["connection_id"] != float64(42) || snapshot["captured_at"] == "" {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if _, ok := decoded["plan"].(map[string]any)["actual"]; !ok {
		t.Fatalf("snapshot has no runtime evidence: %#v", decoded)
	}
}
