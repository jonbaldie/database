package queryexplanation

import (
	"encoding/json"
	"slices"
	"testing"
)

func ordersTable() Table {
	return Table{Database: "app", Name: "orders", Columns: []string{"id", "customer_id", "total"}, RowCount: 3}
}

func decode(t *testing.T, document *Document) map[string]any {
	t.Helper()
	encoded, err := RenderJSON(document)
	if err != nil {
		t.Fatalf("render json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return decoded
}

func collectKinds(node map[string]any, kinds *[]string) {
	*kinds = append(*kinds, node["kind"].(string))
	for _, child := range node["children"].([]any) {
		collectKinds(child.(map[string]any), kinds)
	}
}

func TestSelectEnvelopeAndVocabulary(t *testing.T) {
	document := PlanSelect("0.1.0", "SELECT id FROM orders WHERE customer_id = 7", "app",
		Select{Table: ordersTable(), Columns: []string{"id"}, AllColumns: false, Where: "customer_id = 7"})
	decoded := decode(t, document)

	if decoded["format_version"] != float64(1) || decoded["mode"] != "plan" || decoded["partial"] != false {
		t.Fatalf("unexpected envelope: %#v", decoded)
	}
	statement := decoded["statement"].(map[string]any)
	if statement["kind"] != "select" || statement["read_only"] != true || statement["locking_read"] != false {
		t.Fatalf("unexpected statement: %#v", statement)
	}
	if _, present := statement["planning_settings"].(map[string]any)["sql_mode"]; !present {
		t.Fatal("planning settings missing sql_mode")
	}

	var kinds []string
	collectKinds(decoded["plan"].(map[string]any), &kinds)
	if !slices.Equal(kinds, []string{"project", "filter", "scan"}) {
		t.Fatalf("select operator kinds = %q", kinds)
	}
	assertPublicKinds(t, decoded["plan"].(map[string]any))
	assertUniquePreorderIDs(t, decoded["plan"].(map[string]any))
}

func TestAllColumnsSelectOmitsProjection(t *testing.T) {
	document := PlanSelect("0.1.0", "SELECT * FROM orders", "app",
		Select{Table: ordersTable(), AllColumns: true})
	var kinds []string
	collectKinds(decode(t, document)["plan"].(map[string]any), &kinds)
	if !slices.Equal(kinds, []string{"scan"}) {
		t.Fatalf("all-column select kinds = %q", kinds)
	}
}

func TestWriteKindsUsePublicVocabulary(t *testing.T) {
	cases := map[string]struct {
		write Write
		kinds []string
	}{
		"insert": {Write{Kind: "insert", Table: ordersTable(), ValueRows: 2}, []string{"mutation", "values"}},
		"update": {Write{Kind: "update", Table: ordersTable(), Where: "id = 1"}, []string{"mutation", "filter", "scan"}},
		"delete": {Write{Kind: "delete", Table: ordersTable(), Where: "id = 1"}, []string{"mutation", "filter", "scan"}},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			document := PlanWrite("0.1.0", "SQL", "app", testCase.write)
			decoded := decode(t, document)
			if decoded["statement"].(map[string]any)["read_only"] != false {
				t.Fatal("write reported read_only")
			}
			var kinds []string
			collectKinds(decoded["plan"].(map[string]any), &kinds)
			if !slices.Equal(kinds, testCase.kinds) {
				t.Fatalf("%s kinds = %q, want %q", name, kinds, testCase.kinds)
			}
			assertPublicKinds(t, decoded["plan"].(map[string]any))
			assertUniquePreorderIDs(t, decoded["plan"].(map[string]any))
		})
	}
}

func TestReproducibleForUnchangedInputs(t *testing.T) {
	read := Select{Table: ordersTable(), Columns: []string{"id"}, Where: "customer_id = 7"}
	first, _ := RenderJSON(PlanSelect("0.1.0", "SQL", "app", read))
	second, _ := RenderJSON(PlanSelect("0.1.0", "SQL", "app", read))
	if first != second {
		t.Fatal("unchanged inputs produced different explanations")
	}

	changed := read
	changed.Table.RowCount = 9000
	third, _ := RenderJSON(PlanSelect("0.1.0", "SQL", "app", changed))
	if third == first {
		t.Fatal("changed planning inputs were not identifiable in the explanation")
	}
}

func TestTabularProjectionColumnsAndNulls(t *testing.T) {
	document := PlanSelect("0.1.0", "SELECT id FROM orders WHERE customer_id = 7", "app",
		Select{Table: ordersTable(), Columns: []string{"id"}, Where: "customer_id = 7"})
	tabular := RenderTabular(document)

	for _, required := range []string{"operator_id", "parent_operator_id", "operator", "strategy", "estimated_cost", "actual_rows", "summary", "warnings"} {
		if !slices.Contains(tabular.Columns, required) {
			t.Errorf("tabular projection missing %q", required)
		}
	}
	if len(tabular.Rows) != 3 {
		t.Fatalf("tabular rows = %d, want 3", len(tabular.Rows))
	}
	actualRows := slices.Index(tabular.Columns, "actual_rows")
	if !tabular.Null[0][actualRows] {
		t.Error("plan-only actual_rows is not SQL NULL")
	}
	parentColumn := slices.Index(tabular.Columns, "parent_operator_id")
	if !tabular.Null[0][parentColumn] {
		t.Error("root parent_operator_id is not SQL NULL")
	}
	if tabular.Null[1][parentColumn] {
		t.Error("child parent_operator_id must not be NULL")
	}
}

func assertPublicKinds(t *testing.T, node map[string]any) {
	t.Helper()
	if !slices.Contains(publicKinds(), node["kind"].(string)) {
		t.Errorf("operator %v uses non-public kind %q", node["id"], node["kind"])
	}
	for _, field := range []string{"id", "kind", "summary", "operation", "estimates", "output", "warnings", "children"} {
		if _, present := node[field]; !present {
			t.Errorf("operator %v missing required field %q", node["id"], field)
		}
	}
	for _, child := range node["children"].([]any) {
		assertPublicKinds(t, child.(map[string]any))
	}
}

func assertUniquePreorderIDs(t *testing.T, root map[string]any) {
	t.Helper()
	seen := map[float64]bool{}
	next := float64(1)
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		id := node["id"].(float64)
		if seen[id] {
			t.Errorf("duplicate operator id %v", id)
		}
		if id != next {
			t.Errorf("operator id %v is not pre-order sequential (want %v)", id, next)
		}
		seen[id] = true
		next++
		for _, child := range node["children"].([]any) {
			visit(child.(map[string]any))
		}
	}
	visit(root)
}

func publicKinds() []string {
	return []string{
		"values", "scan", "lookup", "filter", "project", "join", "aggregate", "window",
		"sort", "limit", "distinct", "set_operation", "materialize", "lock", "constraint_check", "mutation",
	}
}
