package mysql

import (
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
