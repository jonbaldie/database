package blackbox_test

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue569AccountTerminationWaitsForRunningStatement(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "termination-secret")
	process, address := startMySQLServer(t, runner, directory, "--lock-wait-timeout-ms=10000")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "termination-secret")
	defer admin.close()
	for _, query := range []string{
		"CREATE DATABASE termination",
		"USE termination",
		"CREATE TABLE records (id INT PRIMARY KEY, value INT)",
		"INSERT INTO records VALUES (1, 10)",
	} {
		mustQuery(t, admin, query)
	}
	for _, test := range []struct {
		name    string
		command string
	}{
		{name: "lockme", command: "ALTER USER 'lockme' ACCOUNT LOCK"},
		{name: "dropme", command: "DROP USER 'dropme'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mustQuery(t, admin, "CREATE USER '"+test.name+"' IDENTIFIED BY 'termination-user-secret'")
			mustQuery(t, admin, "GRANT DATA_READ ON termination.* TO '"+test.name+"'")
			mustQuery(t, admin, "GRANT DATA_WRITE ON termination.* TO '"+test.name+"'")
			owner := newWireClient(t, address, "admin", "termination-secret")
			defer owner.close()
			worker := newWireClient(t, address, test.name, "termination-user-secret")
			defer worker.close()
			idle := newWireClient(t, address, test.name, "termination-user-secret")
			defer idle.close()
			mustQuery(t, idle, "SELECT 1")
			for _, client := range []*wireClient{owner, worker} {
				mustQuery(t, client, "USE termination")
				mustQuery(t, client, "BEGIN")
			}
			mustQuery(t, owner, "SELECT id FROM records WHERE id = 1 FOR UPDATE")
			query := "UPDATE records SET value = 20 WHERE id = 1"
			blocked := queryAsync(worker, query)
			waitForIssue569Query(t, admin, worker.connectionID, query)
			mustQuery(t, admin, test.command)
			waitForProcessListGone(t, admin, idle.connectionID)
			mustRemainBlocked(t, blocked)
			mustQuery(t, owner, "COMMIT")
			select {
			case result := <-blocked:
				mustQueryResult(t, result, "statement in flight during account termination")
			case <-time.After(2 * time.Second):
				t.Fatal("statement did not return after account termination")
			}
			waitForProcessListGone(t, admin, worker.connectionID)
			result := admin.query("SELECT value FROM records WHERE id = 1")
			if result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "10" {
				t.Fatalf("terminated transaction was not rolled back: %#v", result)
			}
		})
	}
}

func waitForIssue569Query(t *testing.T, observer *wireClient, id uint32, query string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result := observer.query("SHOW FULL PROCESSLIST")
		for _, row := range result.rows {
			if len(row) >= 8 && row[0] == strconv.FormatUint(uint64(id), 10) && row[4] == "Query" && row[7] == query {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("connection %d did not start query %q", id, query)
}
