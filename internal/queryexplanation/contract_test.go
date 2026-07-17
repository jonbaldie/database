package queryexplanation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var operatorKinds = []string{
	"values", "scan", "lookup", "filter", "project", "join", "aggregate", "window",
	"sort", "limit", "distinct", "set_operation", "materialize", "lock", "constraint_check", "mutation",
}

func TestPublishedContractExamples(t *testing.T) {
	contract := filepath.Join(repositoryRoot(t), "docs", "query-explanation")
	schema := readJSONObject(t, filepath.Join(contract, "explain-v1.schema.json"))
	if got := schema["$id"]; got != "https://github.com/jonbaldie/database/query-explanation/explain-v1.schema.json" {
		t.Fatalf("schema $id = %v", got)
	}
	assertVersionAndVocabulary(t, schema)
	assertForwardCompatibleSchema(t, schema)

	for name, want := range map[string]struct {
		mode    string
		partial bool
	}{
		"plan.json":     {mode: "plan", partial: false},
		"analyze.json":  {mode: "analyze", partial: false},
		"snapshot.json": {mode: "snapshot", partial: true},
		"write.json":    {mode: "plan", partial: false},
	} {
		document := readJSONObject(t, filepath.Join(contract, "examples", name))
		if document["format_version"] != float64(1) || document["mode"] != want.mode || document["partial"] != want.partial {
			t.Fatalf("%s has incompatible envelope: %#v", name, document)
		}
		if violations := semanticViolations(document); len(violations) > 0 {
			t.Errorf("%s violates semantic rules: %s", name, strings.Join(violations, "; "))
		}
		assertOperator(t, document["plan"], name)
		assertWriteExecutionOrder(t, document, name)
	}

	tabular, err := os.ReadFile(filepath.Join(contract, "examples", "traditional.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"operator_id", "parent_operator_id", "operator", "strategy", "estimated_cost", "actual_rows", "summary", "warnings"} {
		if !strings.Contains(string(tabular), column) {
			t.Errorf("traditional projection is missing %q", column)
		}
	}
}

func TestSemanticRulesRejectInvalidDocuments(t *testing.T) {
	contract := filepath.Join(repositoryRoot(t), "docs", "query-explanation", "examples")

	plan := cloneDocument(t, readJSONObject(t, filepath.Join(contract, "plan.json")))
	plan["plan"].(map[string]any)["actual"] = map[string]any{}
	if got := semanticViolations(plan); len(got) == 0 {
		t.Fatal("plan runtime evidence was accepted")
	}

	analyze := cloneDocument(t, readJSONObject(t, filepath.Join(contract, "analyze.json")))
	delete(analyze["plan"].(map[string]any), "actual")
	if got := semanticViolations(analyze); len(got) == 0 {
		t.Fatal("analyzed plan without runtime evidence was accepted")
	}

	duplicateID := cloneDocument(t, readJSONObject(t, filepath.Join(contract, "plan.json")))
	root := duplicateID["plan"].(map[string]any)
	root["children"].([]any)[0].(map[string]any)["id"] = root["id"]
	if got := semanticViolations(duplicateID); len(got) == 0 {
		t.Fatal("duplicate operator ID was accepted")
	}
}

func assertWriteExecutionOrder(t *testing.T, document map[string]any, name string) {
	t.Helper()
	if name == "write.json" {
		children := document["plan"].(map[string]any)["children"].([]any)
		got := make([]string, 0, len(children))
		for _, child := range children {
			got = append(got, child.(map[string]any)["kind"].(string))
		}
		want := []string{"values", "constraint_check", "mutation", "mutation"}
		if !slices.Equal(got, want) {
			t.Errorf("write example execution-input order = %q, want %q", got, want)
		}
	}
}

func assertVersionAndVocabulary(t *testing.T, schema map[string]any) {
	t.Helper()
	if schema["properties"].(map[string]any)["format_version"].(map[string]any)["const"] != float64(1) {
		t.Fatal("format version is not fixed at 1")
	}
	definitions := schema["$defs"].(map[string]any)
	kinds := definitions["operatorCommon"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]any)
	got := make([]string, len(kinds))
	for i, kind := range kinds {
		got[i] = kind.(string)
	}
	if !slices.Equal(got, operatorKinds) {
		t.Fatalf("operator vocabulary = %q, want %q", got, operatorKinds)
	}
}

func assertForwardCompatibleSchema(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		if value["additionalProperties"] == false || value["unevaluatedProperties"] == false {
			t.Fatalf("schema rejects additive object fields: %#v", value)
		}
		for _, child := range value {
			assertForwardCompatibleSchema(t, child)
		}
	case []any:
		for _, child := range value {
			assertForwardCompatibleSchema(t, child)
		}
	}
}

func assertOperator(t *testing.T, value any, document string) {
	t.Helper()
	node, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s plan node is not an object", document)
	}
	for _, field := range []string{"id", "kind", "summary", "operation", "estimates", "output", "warnings", "children"} {
		if _, ok := node[field]; !ok {
			t.Errorf("%s node %v missing %q", document, node["id"], field)
		}
	}
	if kind, ok := node["kind"].(string); !ok || !slices.Contains(operatorKinds, kind) {
		t.Errorf("%s node has non-public kind %v", document, node["kind"])
	}
	children, ok := node["children"].([]any)
	if !ok {
		t.Errorf("%s node %v children is not an array", document, node["id"])
		return
	}
	for _, child := range children {
		assertOperator(t, child, document)
	}
}

func semanticViolations(document map[string]any) []string {
	mode, _ := document["mode"].(string)
	seenIDs := map[float64]struct{}{}
	violations := []string{}
	var visit func(any)
	visit = func(value any) {
		node, ok := value.(map[string]any)
		if !ok {
			return
		}
		id, ok := node["id"].(float64)
		if !ok || id < 1 || id != float64(int(id)) {
			violations = append(violations, fmt.Sprintf("invalid operator ID %v", node["id"]))
		} else if _, duplicate := seenIDs[id]; duplicate {
			violations = append(violations, fmt.Sprintf("duplicate operator ID %v", id))
		} else {
			seenIDs[id] = struct{}{}
		}
		_, hasActual := node["actual"]
		if mode == "plan" && hasActual {
			violations = append(violations, fmt.Sprintf("plan node %v has runtime evidence", node["id"]))
		}
		if mode == "analyze" && !hasActual {
			violations = append(violations, fmt.Sprintf("analyzed node %v lacks runtime evidence", node["id"]))
		}
		children, _ := node["children"].([]any)
		for _, child := range children {
			visit(child)
		}
	}
	visit(document["plan"])
	return violations
}

func cloneDocument(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func readJSONObject(t *testing.T, filename string) map[string]any {
	t.Helper()
	bytes, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(bytes, &value); err != nil {
		t.Fatalf("decode %s: %v", filename, err)
	}
	return value
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
