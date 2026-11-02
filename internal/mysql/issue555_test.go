package mysql

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestIssue555TypedSessionDefaults(t *testing.T) {
	for _, zone := range []string{"UTC", "+05:30"} {
		t.Run(zone, func(t *testing.T) {
			server := newTestServer(t, Config{TimeZone: zone, LockWaitTimeout: 750 * time.Microsecond, ResourceLimits: ResourceLimits{StatementTimeout: 1500 * time.Microsecond, ExecutionMemoryLimitBytes: 1234, TemporaryStorageLimitBytes: 5678}})
			session := newTestSession(server, testSessionOptions{database: "initial"})
			executor := &textStatementExecutor{session: session}
			check := func() {
				t.Helper()
				if session.timeZone != zone || session.settings.statementTimeout != 1500*time.Microsecond || session.settings.lockWaitTimeout != 750*time.Microsecond || session.settings.executionMemoryLimit != 1234 || session.settings.temporaryStorageLimit != 5678 || session.settings.collationConnection != collation0900AICI || session.autocommitOff || session.isolation != isolationRepeatableRead || session.nextIsolation != isolationRepeatableRead || session.readOnly || session.nextReadOnly {
					t.Fatalf("unexpected defaults: zone=%q settings=%+v transaction=%+v", session.timeZone, session.settings, session.transactionSettings)
				}
			}
			check()
			session.timeZone = "+01:00"
			session.settings = sessionSettings{}
			session.transactionSettings = transactionSettings{autocommitOff: true, isolation: isolationReadCommitted, nextIsolation: isolationReadCommitted, readOnly: true, nextReadOnly: true}
			for _, name := range []string{"autocommit", "transaction_isolation", "transaction_read_only", "time_zone", "collation_connection", "statement_timeout_ms", "lock_wait_timeout_ms", "execution_memory_limit_bytes", "temporary_storage_limit_bytes"} {
				if err := executor.applySessionVariable(name, "DEFAULT"); err != nil {
					t.Fatalf("default %s: %v", name, err)
				}
			}
			check()
			session.timeZone = "+01:00"
			session.settings.statementTimeout = time.Second
			session.autocommitOff = true
			globalZone := zone
			if zone == "UTC" {
				globalZone = "+00:00"
			}
			for name, want := range map[string]string{"global.time_zone": globalZone, "global.statement_timeout_ms": "1", "global.lock_wait_timeout_ms": "0", "global.autocommit": "ON"} {
				got, err := session.sessionVariable(name)
				if err != nil || got != want {
					t.Errorf("%s = %q, %v; want %q", name, got, err, want)
				}
			}
			session.database = "other"
			session.autocommitOff = true
			if err := (&preparedLifecycle{session}).resetConnection(); err != nil {
				t.Fatal(err)
			}
			check()
			if session.database != "initial" {
				t.Errorf("database = %q", session.database)
			}
		})
	}
}

func TestIssue555BooleanAliases(t *testing.T) {
	for _, variable := range []string{"autocommit", "transaction_read_only"} {
		for _, tc := range []struct{ input, want, opposite string }{
			{"0", "OFF", "ON"}, {"off", "OFF", "ON"}, {"false", "OFF", "ON"},
			{"1", "ON", "OFF"}, {"on", "ON", "OFF"}, {"true", "ON", "OFF"},
		} {
			t.Run(variable+"/"+tc.input, func(t *testing.T) {
				executor := newSeededExecutor(t)
				seedSQL(t, executor, "SET "+variable+" = "+tc.opposite, "SET "+variable+" = "+tc.input)
				result, err := executeStatement(executor, "SELECT @@"+variable)
				if err != nil || !reflect.DeepEqual(result.rows, [][]string{{tc.want}}) {
					t.Fatalf("%s = %v, error %v; want %s", variable, result, err, tc.want)
				}
			})
		}
	}
}

func TestIssue555AutocommitTransactionTransitions(t *testing.T) {
	for _, tc := range []struct {
		name, initial, set string
		committed          bool
	}{
		{"off to on commits", "OFF", "ON", true},
		{"off to default commits", "OFF", "DEFAULT", true},
		{"off stays off", "OFF", "OFF", false},
		{"on stays on", "ON", "ON", false},
		{"on to off", "ON", "OFF", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := newSeededExecutor(t, "CREATE TABLE probe (id INT PRIMARY KEY)")
			observer := &textStatementExecutor{session: newTestSession(executor.server, testSessionOptions{database: "app"})}
			seedSQL(t, executor, "SET autocommit = "+tc.initial, "BEGIN", "INSERT INTO probe VALUES (7)", "SET autocommit = "+tc.set)
			want := "[]"
			if tc.committed {
				want = "[[7]]"
			}
			if got := fmt.Sprint(pointLookupQueryRows(t, observer, "SELECT id FROM probe")); got != want {
				t.Fatalf("observer rows = %s, want %s", got, want)
			}
			if executor.session.transaction == tc.committed {
				t.Fatalf("transaction active = %t, committed = %t", executor.session.transaction, tc.committed)
			}
			if !tc.committed {
				if got := fmt.Sprint(pointLookupQueryRows(t, executor, "SELECT id FROM probe")); got != "[[7]]" {
					t.Fatalf("transaction rows = %s, want [[7]]", got)
				}
				seedSQL(t, executor, "ROLLBACK")
				if got := fmt.Sprint(pointLookupQueryRows(t, observer, "SELECT id FROM probe")); got != "[]" {
					t.Fatalf("rows after rollback = %s, want []", got)
				}
			}
		})
	}
}

func TestIssue555ExplicitSessionSettings(t *testing.T) {
	executor := newSeededExecutorWithConfig(t, Config{TimeZone: "UTC", LockWaitTimeout: 10 * time.Second, ResourceLimits: ResourceLimits{StatementTimeout: 10 * time.Second, ExecutionMemoryLimitBytes: 1000000, TemporaryStorageLimitBytes: 1000000}})
	for _, tc := range []struct{ name, input, want string }{
		{"transaction_isolation", "'READ-COMMITTED'", "READ-COMMITTED"},
		{"transaction_isolation", "'REPEATABLE-READ'", "REPEATABLE-READ"},
		{"statement_timeout_ms", "5000", "5000"},
		{"lock_wait_timeout_ms", "4000", "4000"},
		{"execution_memory_limit_bytes", "500000", "500000"},
		{"temporary_storage_limit_bytes", "600000", "600000"},
	} {
		seedSQL(t, executor, "SET "+tc.name+" = "+tc.input)
		result, err := executeStatement(executor, "SELECT @@"+tc.name)
		if err != nil || !reflect.DeepEqual(result.rows, [][]string{{tc.want}}) {
			t.Fatalf("%s = %v, error %v; want %s", tc.name, result, err, tc.want)
		}
	}
}
