package mysql

import (
	"reflect"
	"testing"
)

func TestIssue266LimitZeroOrderBy(t *testing.T) {
	executor := relationalSelectExecutor(t)
	executor.streamRows = true
	resources := newStatementResources(executor.server.resources, executor.server.config, nil)
	executor.session.resources = resources
	defer func() {
		closeStatementResources(resources)
		executor.session.resources = nil
	}()

	t.Run("limit 0 returns empty rows with correct column metadata", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT id, name FROM authors ORDER BY name DESC LIMIT 0")
		if err != nil {
			t.Fatalf("execute ordered LIMIT 0 SELECT: %v", err)
		}
		if len(result.columns) != 2 || result.columns[0] != "id" || result.columns[1] != "name" {
			t.Fatalf("unexpected columns: %v", result.columns)
		}

		var rows [][]string
		if err := result.stream(func(row []string, _ []bool) error {
			rows = append(rows, append([]string(nil), row...))
			return nil
		}); err != nil {
			t.Fatalf("stream ordered LIMIT 0 SELECT: %v", err)
		}

		if len(rows) != 0 {
			t.Fatalf("expected 0 rows for LIMIT 0, got %d rows: %#v", len(rows), rows)
		}
	})

	t.Run("limit 0 with offset returns empty rows", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT name FROM authors ORDER BY name DESC LIMIT 0 OFFSET 2")
		if err != nil {
			t.Fatalf("execute ordered LIMIT 0 OFFSET 2 SELECT: %v", err)
		}

		var rows [][]string
		if err := result.stream(func(row []string, _ []bool) error {
			rows = append(rows, append([]string(nil), row...))
			return nil
		}); err != nil {
			t.Fatalf("stream ordered LIMIT 0 OFFSET 2 SELECT: %v", err)
		}

		if len(rows) != 0 {
			t.Fatalf("expected 0 rows for LIMIT 0 OFFSET 2, got %d rows: %#v", len(rows), rows)
		}
	})

	t.Run("positive limit preserves expected rows", func(t *testing.T) {
		result, err := executeStatement(executor, "SELECT name FROM authors ORDER BY name DESC LIMIT 2")
		if err != nil {
			t.Fatalf("execute ordered LIMIT 2 SELECT: %v", err)
		}

		var rows [][]string
		if err := result.stream(func(row []string, _ []bool) error {
			rows = append(rows, append([]string(nil), row...))
			return nil
		}); err != nil {
			t.Fatalf("stream ordered LIMIT 2 SELECT: %v", err)
		}

		want := [][]string{{"Linus"}, {"Grace"}}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("rows = %#v, want %#v", rows, want)
		}
	})
}
