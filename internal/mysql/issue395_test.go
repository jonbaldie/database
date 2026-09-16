package mysql

import (
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIssue395StatementTimeoutEnforcedDuringLike(t *testing.T) {
	timeout := 50 * time.Millisecond
	resources := newStatementResources(nil, Config{ResourceLimits: ResourceLimits{StatementTimeout: timeout}}, nil)
	session := &session{resources: resources}
	defer closeStatementResources(resources)

	value := strings.Repeat("a", 16384)
	pattern := strings.Repeat("%b", 16384)
	expr := "'" + value + "' LIKE '" + pattern + "'"

	started := time.Now()
	_, err := evaluateScalarResolved(expr, nil, session)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatalf("evaluateScalarResolved succeeded; want timeout error 3024")
	}
	if !isFailureCode(err, 3024) {
		t.Fatalf("evaluateScalarResolved error = %v; want error 3024", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("evaluation took %s with %s timeout; want under 200ms", elapsed, timeout)
	}
}

func TestIssue395DoubledInputDoesNotIncreaseTimeoutDuration(t *testing.T) {
	timeout := 50 * time.Millisecond
	resources := newStatementResources(nil, Config{ResourceLimits: ResourceLimits{StatementTimeout: timeout}}, nil)
	session := &session{resources: resources}
	defer closeStatementResources(resources)

	// Doubled inputs (98 KB variant).
	value := strings.Repeat("a", 32768)
	pattern := strings.Repeat("%b", 32768)
	expr := "'" + value + "' LIKE '" + pattern + "'"

	started := time.Now()
	_, err := evaluateScalarResolved(expr, nil, session)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatalf("evaluateScalarResolved succeeded; want timeout error 3024")
	}
	if !isFailureCode(err, 3024) {
		t.Fatalf("evaluateScalarResolved error = %v; want error 3024", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("evaluation took %s with %s timeout; want under 200ms", elapsed, timeout)
	}
}

func TestIssue395CancellationDuringLikeEvaluation(t *testing.T) {
	cancelled := make(chan struct{})
	close(cancelled)

	resources := newStatementResources(nil, Config{ResourceLimits: ResourceLimits{StatementTimeout: time.Hour}}, cancelled)
	session := &session{resources: resources}
	defer closeStatementResources(resources)

	value := strings.Repeat("a", 16384)
	pattern := strings.Repeat("%b", 16384)
	expr := "'" + value + "' LIKE '" + pattern + "'"

	_, err := evaluateScalarResolved(expr, nil, session)
	if err == nil {
		t.Fatalf("evaluateScalarResolved succeeded; want cancellation error 1317")
	}
	if !isFailureCode(err, 1317) {
		t.Fatalf("evaluateScalarResolved error = %v; want cancellation error 1317", err)
	}
}

func TestIssue395ExecutionThroughPolicyKeepsSessionUsable(t *testing.T) {
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	session := &session{server: server, database: "app", initialDB: "app", timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{}}
	executor := &textStatementExecutor{session: session}

	timeout := 50 * time.Millisecond
	resources := newStatementResources(server.resources, Config{ResourceLimits: ResourceLimits{StatementTimeout: timeout}}, nil)
	session.resources = resources

	value := strings.Repeat("a", 16384)
	pattern := strings.Repeat("%b", 16384)
	query := "SELECT '" + value + "' LIKE '" + pattern + "'"

	started := time.Now()
	_, err = executeStatement(executor, query)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatalf("executeStatement succeeded; want timeout error 3024")
	}
	if !isFailureCode(err, 3024) {
		t.Fatalf("executeStatement error = %v; want error 3024", err)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("statement took %s with %s timeout; want under 150ms", elapsed, timeout)
	}

	closeStatementResources(resources)
	session.resources = nil

	// Session remains usable for subsequent queries.
	result, err := executeStatement(executor, "SELECT 1")
	if err != nil {
		t.Fatalf("executeStatement subsequent SELECT 1 failed: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("executeStatement subsequent SELECT 1 = %#v", result.rows)
	}
}

func TestIssue395LikeSemanticsUnchangedWithoutTimeout(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"'hello' LIKE 'h_llo'", "1"},
		{"'hello' LIKE 'h%o'", "1"},
		{"'hello' NOT LIKE 'h%o'", "0"},
		{"'HELLO' LIKE 'hello'", "1"},
		{"CAST('HELLO' AS BINARY) LIKE 'hello'", "0"},
		{"'hello' LIKE NULL", "NULL"},
		{"NULL LIKE 'hello'", "NULL"},
		{"'100%' LIKE '100!%' ESCAPE '!'", "1"},
		{"'100a' LIKE '100!%' ESCAPE '!'", "0"},
	}

	for _, tc := range cases {
		val, err := evaluateScalar(tc.query)
		if err != nil {
			t.Fatalf("evaluateScalar(%q) error: %v", tc.query, err)
		}
		if tc.want == "NULL" {
			if !val.isNull() {
				t.Errorf("evaluateScalar(%q) = %v; want NULL", tc.query, val.render())
			}
		} else {
			if val.render() != tc.want {
				t.Errorf("evaluateScalar(%q) = %q; want %q", tc.query, val.render(), tc.want)
			}
		}
	}
}
