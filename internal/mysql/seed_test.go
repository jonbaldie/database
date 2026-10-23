package mysql

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

// newSeededExecutor opens an empty catalog, starts a server over it, and
// returns an executor whose session uses database app. It creates app and
// then runs statements through seedSQL.
func newSeededExecutor(tb testing.TB, statements ...string) *textStatementExecutor {
	tb.Helper()
	store, err := catalog.Open(tb.TempDir())
	if err != nil {
		tb.Fatalf("open catalog: %v", err)
	}
	server := newTestServer(tb, Config{Catalog: store, Version: "0.1.0"})
	executor := &textStatementExecutor{session: newTestSession(server, testSessionOptions{database: "app"})}
	seedSQL(tb, executor, append([]string{"CREATE DATABASE app", "USE app"}, statements...)...)
	return executor
}

// seedSQL runs setup statements through the same statement policy as the
// MySQL wire protocol. Seeded schema and rows therefore pass the table
// builder, value coercion, constraints, and durable publication, so a test
// cannot start from a state that a client cannot reach.
func seedSQL(tb testing.TB, executor *textStatementExecutor, statements ...string) {
	tb.Helper()
	for _, statement := range statements {
		if _, err := executeStatement(executor, statement); err != nil {
			tb.Fatalf("seed %q: %v", statement, err)
		}
	}
}

func TestSeedSQLAppliesProductionSchemaAndWriteRules(t *testing.T) {
	executor := newSeededExecutor(t,
		"CREATE TABLE probe (id INT PRIMARY KEY, d DECIMAL(5,2) NOT NULL)",
		"INSERT INTO probe VALUES (1, 1.5)",
	)

	if _, err := executeStatement(executor, "INSERT INTO probe VALUES ('abc', 1.5)"); err == nil || !strings.Contains(err.Error(), "Incorrect integer value") {
		t.Fatalf("invalid INT error = %v, want Incorrect integer value", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO probe VALUES (1, 2.5)"); err == nil || !strings.Contains(err.Error(), "Duplicate entry") {
		t.Fatalf("duplicate primary key error = %v, want Duplicate entry", err)
	}

	result, err := executeStatement(executor, "SELECT id, d FROM probe WHERE id = 1")
	if err != nil {
		t.Fatalf("select seeded row: %v", err)
	}
	if want := [][]string{{"1", "1.50"}}; !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}

	table := executor.server.config.Catalog.Snapshot().Namespaces[catalog.Key("app")].Tables[catalog.Key("probe")]
	if want := []catalog.ColumnAttribute{{Nullable: false}, {Nullable: false}}; !reflect.DeepEqual(table.ColumnAttributes, want) {
		t.Fatalf("column attributes = %#v, want %#v", table.ColumnAttributes, want)
	}
	if len(table.Constraints) != 1 || table.Constraints[0].Type != catalog.ConstraintTypePrimary || !reflect.DeepEqual(table.Constraints[0].Columns, []string{"id"}) {
		t.Fatalf("constraints = %#v, want one primary key on id", table.Constraints)
	}
}
