package mysql

import (
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestShutdownRequiresOperationalControl(t *testing.T) {
	executor := backupExecutor(t, "reader", []catalog.Grant{{Privilege: "OPERATIONAL_OBSERVATION"}})
	_, err := executeStatement(executor, "SHUTDOWN")
	failure, ok := err.(sqlFailure)
	if !ok || failure.code != 1227 {
		t.Fatalf("expected access denied, got %#v", err)
	}
}

func TestShutdownRequestsServerStopAndReturnsIdentity(t *testing.T) {
	executor := backupExecutor(t, "admin", nil)
	result, err := executeStatement(executor, "SHUTDOWN 'op-shutdown-1'")
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if len(result.columns) != 2 || result.columns[0] != "instance_id" || result.columns[1] != "requested_at" {
		t.Fatalf("columns = %#v", result.columns)
	}
	if len(result.rows) != 1 || result.rows[0][0] != "source-live" {
		t.Fatalf("rows = %#v", result.rows)
	}
	if _, err := time.Parse(time.RFC3339Nano, result.rows[0][1]); err != nil {
		t.Fatalf("requested_at = %q: %v", result.rows[0][1], err)
	}
	select {
	case <-executor.server.ShutdownRequested():
	case <-time.After(time.Second):
		t.Fatal("shutdown was not requested")
	}
	if executor.server.ShutdownOperationID() != "op-shutdown-1" {
		t.Fatalf("shutdown operation id = %q", executor.server.ShutdownOperationID())
	}
}
