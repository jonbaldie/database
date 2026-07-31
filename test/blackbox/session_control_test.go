package blackbox_test

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLSessionObservationAndQueryCancellation(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "session-control-secret")
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "session-control-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE DATABASE control_data")
	mustQuery(t, admin, "USE control_data")
	mustQuery(t, admin, "CREATE TABLE records (id INT)")
	mustQuery(t, admin, "INSERT INTO records VALUES (1)")
	mustQuery(t, admin, "CREATE USER 'writer' IDENTIFIED BY 'session-writer-secret'")
	mustQuery(t, admin, "GRANT DATA_WRITE ON control_data.* TO 'writer'")
	mustQuery(t, admin, "GRANT DATA_READ ON control_data.* TO 'writer'")
	mustQuery(t, admin, "CREATE USER 'observer' IDENTIFIED BY 'session-observer-secret'")
	mustQuery(t, admin, "GRANT OPERATIONAL_OBSERVATION ON *.* TO 'observer'")
	mustQuery(t, admin, "GRANT OPERATIONAL_CONTROL ON *.* TO 'observer'")

	owner := newWireClient(t, address, "admin", "session-control-secret")
	defer owner.close()
	mustQuery(t, owner, "USE control_data")
	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM records WHERE id = 1 FOR UPDATE")

	writer := newWireClient(t, address, "writer", "session-writer-secret")
	defer writer.close()
	mustQuery(t, writer, "USE control_data")
	writerControl := newWireClient(t, address, "writer", "session-writer-secret")
	defer writerControl.close()
	observer := newWireClient(t, address, "observer", "session-observer-secret")
	defer observer.close()

	if result := writerControl.query("KILL QUERY " + strconv.FormatUint(uint64(owner.connectionID), 10)); result.errCode != 1094 {
		t.Fatalf("unprivileged cross-account kill: %#v", result)
	}
	if users := processListUsers(t, writerControl.query("SHOW PROCESSLIST")); users["admin"] || users["observer"] || !users["writer"] {
		t.Fatalf("unprivileged process list: %#v", users)
	}

	pending := queryAsync(writer, "UPDATE records SET id = 2 WHERE id = 1")
	waitForProcessListQuery(t, writerControl, writer.connectionID)
	mustQuery(t, writerControl, "KILL QUERY "+strconv.FormatUint(uint64(writer.connectionID), 10))
	result := <-pending
	if result.errCode != 1317 {
		t.Fatalf("cancelled own writer result: %#v", result)
	}

	pending = queryAsync(writer, "UPDATE records SET id = 2 WHERE id = 1")
	waitForProcessListQuery(t, observer, writer.connectionID)
	mustQuery(t, observer, "KILL QUERY "+strconv.FormatUint(uint64(writer.connectionID), 10))
	if result := <-pending; result.errCode != 1317 {
		t.Fatalf("cancelled elevated writer result: %#v", result)
	}
	if result := writer.query("SELECT 1"); result.err != "" {
		t.Fatalf("writer session after cancellation: %#v", result)
	}

	mustQuery(t, owner, "COMMIT")
	mustQuery(t, writer, "BEGIN")
	mustQuery(t, writer, "INSERT INTO records VALUES (2)")
	mustQuery(t, observer, "KILL CONNECTION "+strconv.FormatUint(uint64(writer.connectionID), 10))
	waitForProcessListGone(t, observer, writer.connectionID)
	if result := admin.query("SELECT id FROM records WHERE id = 2"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("killed session transaction was not rolled back: %#v", result)
	}
}

func processListHasQuery(result wireResult, connectionID uint32) bool {
	for _, row := range result.rows {
		if len(row) > 4 && row[0] == strconv.FormatUint(uint64(connectionID), 10) && row[4] == "Query" {
			return true
		}
	}
	return false
}

func processListUsers(t *testing.T, result wireResult) map[string]bool {
	t.Helper()
	if result.err != "" {
		t.Fatalf("show processlist: %#v", result)
	}
	users := make(map[string]bool)
	for _, row := range result.rows {
		if len(row) > 1 {
			users[row[1]] = true
		}
	}
	return users
}

func waitForProcessListQuery(t *testing.T, client *wireClient, connectionID uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		result := client.query("SHOW PROCESSLIST")
		if result.err != "" {
			t.Fatalf("show processlist: %#v", result)
		}
		if processListHasQuery(result, connectionID) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection %d was not visible as an active query: %#v", connectionID, result)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProcessListGone(t *testing.T, client *wireClient, connectionID uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		result := client.query("SHOW PROCESSLIST")
		if result.err != "" {
			t.Fatalf("show processlist: %#v", result)
		}
		if !processListHasConnection(result, connectionID) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection %d did not end: %#v", connectionID, result)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processListHasConnection(result wireResult, connectionID uint32) bool {
	for _, row := range result.rows {
		if len(row) > 0 && row[0] == strconv.FormatUint(uint64(connectionID), 10) {
			return true
		}
	}
	return false
}
