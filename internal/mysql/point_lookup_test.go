package mysql

import (
	"fmt"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/queryexplanation"
)

func TestLiveExplanationDoesNotDisablePointLookup(t *testing.T) {
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })

	executor := &textStatementExecutor{session: &session{
		server: server, database: "app", initialDB: "app", connectionID: 7,
		timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{},
	}}
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE items (id INT PRIMARY KEY, label VARCHAR(32) NOT NULL)",
		"INSERT INTO items VALUES (7, 'grace')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	relations := &relationExecutor{session: executor.session}
	plan, err := parseRelationalSelect(relations, "SELECT id, label FROM items WHERE id = 7")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	finish := server.explanations.begin(executor.session.connectionID, &queryexplanation.Document{}, executor.session)
	defer finish()
	if executor.session.runtimeMetrics != nil {
		t.Fatal("live explanation must not attach runtime metrics that disable point lookup")
	}
	result, ok := tryRelationalPointLookup(plan)
	if !ok {
		t.Fatal("expected point lookup during live explanation")
	}
	if len(result.rows) != 1 || result.rows[0][0] != "7" || result.rows[0][1] != "grace" {
		t.Fatalf("point lookup rows = %#v", result.rows)
	}
}

func newPointLookupTestExecutor(t *testing.T) *textStatementExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	executor := &textStatementExecutor{session: &session{
		server: server, database: "app", initialDB: "app", connectionID: 7,
		timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{},
	}}
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE items (id INT PRIMARY KEY, code VARCHAR(8) NOT NULL UNIQUE, label VARCHAR(32) NOT NULL)",
		"INSERT INTO items VALUES (7, 'g', 'grace'), (9, 'a', 'ada')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	return executor
}

func pointLookupQueryRows(t *testing.T, executor *textStatementExecutor, query string) [][]string {
	t.Helper()
	result, err := executeStatement(executor, query)
	if err != nil {
		t.Fatalf("%q: %v", query, err)
	}
	result, err = materializeQueryResult(result)
	if err != nil {
		t.Fatalf("materialize %q: %v", query, err)
	}
	return result.rows
}

func TestPointLookupSeesTransactionStagedRows(t *testing.T) {
	executor := newPointLookupTestExecutor(t)
	for _, query := range []string{
		"BEGIN",
		"INSERT INTO items VALUES (8, 'h', 'hopper')",
		"DELETE FROM items WHERE id = 7",
		"UPDATE items SET label = 'lovelace' WHERE id = 9",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%q: %v", query, err)
		}
	}
	for _, check := range []struct {
		query string
		want  string
	}{
		{"SELECT id, label FROM items WHERE id = 8", "[[8 hopper]]"},
		{"SELECT id, label FROM items WHERE code = 'h'", "[[8 hopper]]"},
		{"SELECT id, label FROM items WHERE id = 7", "[]"},
		{"SELECT id, label FROM items WHERE code = 'g'", "[]"},
		{"SELECT id, label FROM items WHERE id = 9", "[[9 lovelace]]"},
		{"SELECT id, label FROM items WHERE code = 'a'", "[[9 lovelace]]"},
	} {
		if got := fmt.Sprint(pointLookupQueryRows(t, executor, check.query)); got != check.want {
			t.Errorf("in transaction %q = %s, want %s", check.query, got, check.want)
		}
	}
	if _, err := executeStatement(executor, "ROLLBACK"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := fmt.Sprint(pointLookupQueryRows(t, executor, "SELECT id, label FROM items WHERE id = 7")); got != "[[7 grace]]" {
		t.Errorf("after rollback id = 7 = %s", got)
	}
}

func TestRuntimeMetricsDoNotChangePointLookupRows(t *testing.T) {
	executor := newPointLookupTestExecutor(t)
	for _, query := range []string{"BEGIN", "DELETE FROM items WHERE id = 7"} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("%q: %v", query, err)
		}
	}
	relations := &relationExecutor{session: executor.session}
	lookup := func() string {
		plan, err := parseRelationalSelect(relations, "SELECT id, label FROM items WHERE id = 7")
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		result, err := executeRelationalSelectPlan(plan, false)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return fmt.Sprint(result.rows)
	}
	without := lookup()
	executor.session.runtimeMetrics = &queryexplanation.RuntimeMetrics{}
	with := lookup()
	executor.session.runtimeMetrics = nil
	if without != with || without != "[]" {
		t.Fatalf("rows without metrics = %s, with metrics = %s, want []", without, with)
	}
}
