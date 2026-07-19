package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func expressionExecutor(t *testing.T) *textStatementExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	session := &session{server: server, database: "app", initialDB: "app", timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{}}
	return &textStatementExecutor{session}
}

// TestSelectExpressionThroughSQL exercises the full text-execution seam so the
// scalar engine is reached exactly as a client SELECT reaches it.
func TestSelectExpressionThroughSQL(t *testing.T) {
	executor := expressionExecutor(t)
	cases := map[string]string{
		"SELECT 1 + 1":                  "2",
		"SELECT 7 / 2":                  "3.5000",
		"SELECT CONCAT('a', 'b')":       "ab",
		"SELECT CAST('42' AS SIGNED)":   "42",
		"SELECT 3 > 2 AND 1 = 1":        "1",
		"SELECT ABS(-9)":                "9",
		"SELECT COALESCE(NULL, 'last')": "last",
	}
	for query, want := range cases {
		result, err := executor.execute(query)
		if err != nil {
			t.Fatalf("execute(%q) error: %v", query, err)
		}
		if len(result.rows) != 1 || result.rows[0][0] != want {
			t.Errorf("execute(%q) = %#v, want single row %q", query, result.rows, want)
		}
	}
}

func TestSelectExpressionFailsClosedThroughSQL(t *testing.T) {
	executor := expressionExecutor(t)
	for _, query := range []string{
		"SELECT 1 / 0",
		"SELECT NOSUCHFN(1)",
		"SELECT 'a' + 1",
	} {
		if _, err := executor.execute(query); err == nil {
			t.Errorf("execute(%q) expected an error", query)
		}
	}
}

func TestSelectNullExpressionReportsNull(t *testing.T) {
	executor := expressionExecutor(t)
	result, err := executor.execute("SELECT NULLIF(1, 1)")
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if len(result.nulls) != 1 || len(result.nulls[0]) != 1 || !result.nulls[0][0] {
		t.Fatalf("SELECT NULLIF(1,1) did not report a NULL row: %#v", result.nulls)
	}
}
