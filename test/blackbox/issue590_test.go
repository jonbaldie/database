package blackbox_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLIndexCoverageOpportunityMatchesProjectionClassification(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "coverage-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "coverage-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE coverage",
		"USE coverage",
		"CREATE TABLE tasks (id BIGINT PRIMARY KEY, title VARCHAR(100) NOT NULL, priority INT NOT NULL)",
		"INSERT INTO tasks VALUES (1, 'read the plan', 1), (2, 'test query behaviour', 2)",
	} {
		mustQuery(t, client, query)
	}

	cases := []struct {
		name, query, strategy string
		rows                  [][]string
		coverageOpportunity   bool
	}{
		{"covering", "SELECT id FROM tasks WHERE priority >= 2 ORDER BY id", "btree_covering_index_scan", [][]string{{"2"}}, false},
		{"non-covering", "SELECT title FROM tasks WHERE priority >= 2 ORDER BY id", "btree_index_scan", [][]string{{"test query behaviour"}}, true},
		{"all-columns", "SELECT * FROM tasks WHERE priority >= 2 ORDER BY id", "btree_index_scan", [][]string{{"2", "test query behaviour", "2"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := client.query(tc.query)
			if result.err != "" || !slices.EqualFunc(result.rows, tc.rows, slices.Equal) {
				t.Fatalf("query result: %#v, want rows %#v", result, tc.rows)
			}
			for _, mode := range []string{"EXPLAIN FORMAT=JSON", "EXPLAIN ANALYZE FORMAT=JSON"} {
				t.Run(mode, func(t *testing.T) {
					result := client.query(mode + " " + tc.query)
					if result.err != "" || len(result.rows) != 1 || len(result.rows[0]) != 1 {
						t.Fatalf("explanation: %#v", result)
					}
					var document map[string]any
					if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
						t.Fatal(err)
					}
					scan := findPublicPlanKind(t, document["plan"], "scan")
					strategy, ok := scan["strategy"].(map[string]any)
					if !ok || strategy["name"] != tc.strategy {
						t.Fatalf("scan strategy: %#v, want %s", scan["strategy"], tc.strategy)
					}
					opportunities, present := scan["opportunities"]
					if !tc.coverageOpportunity {
						if present {
							t.Errorf("covering scan has opportunities: %#v", opportunities)
						}
						return
					}
					list, ok := opportunities.([]any)
					if !ok || len(list) != 1 {
						t.Fatalf("non-covering scan opportunities: %#v", opportunities)
					}
					opportunity, ok := list[0].(map[string]any)
					if !ok || opportunity["code"] != "INDEX_COVERAGE" || !slices.EqualFunc(asStrings(opportunity["evidence"]), []string{"The selected index is classified as non-covering for the projection."}, func(a, b string) bool { return a == b }) {
						t.Errorf("coverage opportunity: %#v", list[0])
					}
				})
			}
		})
	}
}

func asStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		result = append(result, text)
	}
	return result
}
