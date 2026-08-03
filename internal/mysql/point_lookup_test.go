package mysql

import (
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
		if _, err := executor.execute(query); err != nil {
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
