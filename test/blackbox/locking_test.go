package blackbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLCoordinatesConcurrentLocks(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "lock-secret")
	process, address := startMySQLServer(t, runner, directory, "--lock-wait-timeout-ms=500")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "lock-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE DATABASE coordination")
	mustQuery(t, admin, "USE coordination")

	for _, isolation := range []string{"REPEATABLE READ", "READ COMMITTED"} {
		t.Run(isolation, func(t *testing.T) {
			table := "work_" + map[string]string{"REPEATABLE READ": "repeatable", "READ COMMITTED": "committed"}[isolation]
			mustQuery(t, admin, "CREATE TABLE "+table+" (id INT PRIMARY KEY, value INT)")
			mustQuery(t, admin, "INSERT INTO "+table+" VALUES (1, 10)")

			owner := newWireClient(t, address, "admin", "lock-secret")
			defer owner.close()
			writer := newWireClient(t, address, "admin", "lock-secret")
			defer writer.close()
			observer := newWireClient(t, address, "admin", "lock-secret")
			defer observer.close()
			setIsolation(t, owner, isolation)
			setIsolation(t, writer, isolation)
			mustQuery(t, owner, "USE coordination")
			mustQuery(t, writer, "USE coordination")
			mustQuery(t, observer, "USE coordination")
			mustQuery(t, owner, "BEGIN")
			mustQuery(t, owner, "SELECT id FROM "+table+" WHERE id = 1 FOR UPDATE")
			mustQuery(t, writer, "BEGIN")

			completed := queryAsync(writer, "UPDATE "+table+" SET value = 20 WHERE id = 1")
			mustRemainBlocked(t, completed)
			if result := observer.query("SELECT value FROM " + table + " WHERE id = 1"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "10" {
				t.Fatalf("uncommitted write visibility: %#v", result)
			}
			mustQuery(t, owner, "COMMIT")
			mustQueryResult(t, <-completed, "waiting write")
			mustQuery(t, writer, "COMMIT")
			if result := observer.query("SELECT value FROM " + table + " WHERE id = 1"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "20" {
				t.Fatalf("committed write visibility: %#v", result)
			}
		})
	}
}

func TestMySQLLockModesTimeoutCancellationAndDeadlock(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "lock-secret")
	process, address := startMySQLServer(t, runner, directory, "--lock-wait-timeout-ms=500")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "lock-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE DATABASE coordination")
	mustQuery(t, admin, "USE coordination")
	mustQuery(t, admin, "CREATE TABLE work (id INT PRIMARY KEY, value INT)")
	mustQuery(t, admin, "INSERT INTO work VALUES (1, 10), (2, 10), (3, 10)")
	explained := admin.query("EXPLAIN FORMAT=JSON SELECT id FROM work FOR UPDATE NOWAIT")
	if explained.err != "" || len(explained.rows) != 1 {
		t.Fatalf("locking EXPLAIN result: %#v", explained)
	}
	var explanation struct {
		Statement struct {
			ReadOnly    bool `json:"read_only"`
			LockingRead bool `json:"locking_read"`
		} `json:"statement"`
	}
	if err := json.Unmarshal([]byte(explained.rows[0][0]), &explanation); err != nil {
		t.Fatalf("decode locking EXPLAIN: %v", err)
	}
	if explanation.Statement.ReadOnly || !explanation.Statement.LockingRead ||
		!strings.Contains(explained.rows[0][0], `"kind":"lock"`) ||
		!strings.Contains(explained.rows[0][0], `"mode":"update"`) ||
		!strings.Contains(explained.rows[0][0], `"wait_policy":"nowait"`) {
		t.Fatalf("locking EXPLAIN document: %#v", explained)
	}

	owner := newWireClient(t, address, "admin", "lock-secret")
	defer owner.close()
	nowait := newWireClient(t, address, "admin", "lock-secret")
	defer nowait.close()
	skip := newWireClient(t, address, "admin", "lock-secret")
	defer skip.close()
	for _, client := range []*wireClient{owner, nowait, skip} {
		mustQuery(t, client, "USE coordination")
	}
	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM work WHERE id = 1 FOR SHARE")
	mustQuery(t, nowait, "BEGIN")
	if result := nowait.query("SELECT id FROM work WHERE id = 1 FOR UPDATE NOWAIT"); result.errCode != 3572 {
		t.Fatalf("NOWAIT result: %#v", result)
	}
	mustQuery(t, nowait, "SELECT id FROM work WHERE id = 2")
	mustQuery(t, nowait, "ROLLBACK")
	mustQuery(t, skip, "BEGIN")
	if result := skip.query("SELECT id FROM work ORDER BY id FOR UPDATE SKIP LOCKED"); result.err != "" || len(result.rows) != 2 || result.rows[0][0] != "2" || result.rows[1][0] != "3" {
		t.Fatalf("SKIP LOCKED result: %#v", result)
	}
	mustQuery(t, skip, "ROLLBACK")

	timeout := newWireClient(t, address, "admin", "lock-secret")
	defer timeout.close()
	mustQuery(t, timeout, "USE coordination")
	mustQuery(t, timeout, "BEGIN")
	if result := timeout.query("UPDATE work SET value = 30 WHERE id = 1"); result.errCode != 1205 {
		t.Fatalf("lock wait timeout result: %#v", result)
	}
	mustQuery(t, timeout, "SELECT value FROM work WHERE id = 1")
	mustQuery(t, timeout, "ROLLBACK")

	cancelled := newWireClient(t, address, "admin", "lock-secret")
	mustQuery(t, cancelled, "USE coordination")
	mustQuery(t, cancelled, "BEGIN")
	mustQuery(t, cancelled, "UPDATE work SET value = 40 WHERE id = 2")
	writeWirePacket(t, cancelled.conn, 0, []byte("\x03UPDATE work SET value = 40 WHERE id = 1"))
	if err := cancelled.conn.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled.conn = nil
	assertCancelledLockReleased(t, address, "2")
	mustQuery(t, owner, "COMMIT")
	if result := admin.query("SELECT value FROM work WHERE id = 2"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "10" {
		t.Fatalf("cancelled transaction state: %#v", result)
	}
	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM work WHERE id = 2 FOR UPDATE")
	mustQuery(t, nowait, "BEGIN")
	if result := nowait.query("SELECT id FROM work ORDER BY id FOR UPDATE NOWAIT"); result.errCode != 3572 {
		t.Fatalf("partial NOWAIT result: %#v", result)
	}
	if result := admin.query("SELECT id FROM work WHERE id = 1 FOR UPDATE NOWAIT"); result.err != "" {
		t.Fatalf("partial NOWAIT retained a lock: %#v", result)
	}
	mustQuery(t, nowait, "ROLLBACK")
	mustQuery(t, owner, "ROLLBACK")
	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM work WHERE id = 1 FOR UPDATE")
	mustQuery(t, skip, "BEGIN")
	if result := skip.query("SELECT id FROM work ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "2" {
		t.Fatalf("limited SKIP LOCKED result: %#v", result)
	}
	if result := admin.query("SELECT id FROM work WHERE id = 3 FOR UPDATE NOWAIT"); result.err != "" {
		t.Fatalf("limited SKIP LOCKED retained an omitted lock: %#v", result)
	}
	mustQuery(t, skip, "ROLLBACK")
	mustQuery(t, owner, "ROLLBACK")

	first := newWireClient(t, address, "admin", "lock-secret")
	defer first.close()
	second := newWireClient(t, address, "admin", "lock-secret")
	defer second.close()
	checker := newWireClient(t, address, "admin", "lock-secret")
	defer checker.close()
	for _, client := range []*wireClient{first, second, checker} {
		mustQuery(t, client, "USE coordination")
		mustQuery(t, client, "BEGIN")
	}
	mustQuery(t, first, "UPDATE work SET value = 11 WHERE id = 1")
	mustQuery(t, second, "UPDATE work SET value = 22 WHERE id = 2")
	waiter := queryAsync(first, "UPDATE work SET value = 12 WHERE id = 2")
	mustRemainBlocked(t, waiter)
	if result := second.query("UPDATE work SET value = 21 WHERE id = 1"); result.errCode != 1213 {
		t.Fatalf("deadlock victim result: %#v", result)
	}
	mustQueryResult(t, <-waiter, "deadlock survivor")
	mustQuery(t, first, "COMMIT")
	mustQuery(t, second, "COMMIT")
	mustQuery(t, checker, "COMMIT")
	if result := checker.query("SELECT id, value FROM work WHERE id < 3 ORDER BY id"); result.err != "" || len(result.rows) != 2 || result.rows[0][1] != "11" || result.rows[1][1] != "12" {
		t.Fatalf("deadlock transaction state: %#v", result)
	}
	locker := newWireClient(t, address, "admin", "lock-secret")
	defer locker.close()
	shifter := newWireClient(t, address, "admin", "lock-secret")
	defer shifter.close()
	for _, client := range []*wireClient{locker, shifter} {
		mustQuery(t, client, "USE coordination")
	}
	mustQuery(t, locker, "BEGIN")
	mustQuery(t, locker, "SELECT id FROM work WHERE id = 2 FOR UPDATE")
	mustQuery(t, shifter, "DELETE FROM work WHERE id = 1")
	mustQuery(t, nowait, "BEGIN")
	if result := nowait.query("SELECT id FROM work WHERE id = 2 FOR UPDATE NOWAIT"); result.errCode != 3572 {
		t.Fatalf("row lock after unrelated delete: %#v", result)
	}
	mustQuery(t, nowait, "ROLLBACK")
	mustQuery(t, locker, "ROLLBACK")
}

func setIsolation(t *testing.T, client *wireClient, isolation string) {
	t.Helper()
	mustQuery(t, client, "SET SESSION TRANSACTION ISOLATION LEVEL "+isolation)
}

func queryAsync(client *wireClient, query string) <-chan wireResult {
	completed := make(chan wireResult, 1)
	go func() { completed <- client.query(query) }()
	return completed
}

func mustRemainBlocked(t *testing.T, completed <-chan wireResult) {
	t.Helper()
	select {
	case result := <-completed:
		t.Fatalf("query did not wait: %#v", result)
	case <-time.After(25 * time.Millisecond):
	}
}

func mustQuery(t *testing.T, client *wireClient, query string) {
	t.Helper()
	mustQueryResult(t, client.query(query), query)
}

func mustQueryResult(t *testing.T, result wireResult, action string) {
	t.Helper()
	if result.err != "" {
		t.Fatalf("%s: %#v", action, result)
	}
}

func assertCancelledLockReleased(t *testing.T, address, id string) {
	t.Helper()
	client := newWireClient(t, address, "admin", "lock-secret")
	defer client.close()
	mustQuery(t, client, "USE coordination")
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		result := client.query("SELECT id FROM work WHERE id = " + id + " FOR UPDATE NOWAIT")
		if result.err == "" {
			return
		}
		if result.errCode != 3572 || time.Now().After(deadline) {
			t.Fatalf("cancelled lock state: %#v", result)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func initializeServer(t *testing.T, runner blackbox.Runner, directory, password string) {
	t.Helper()
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize server: %#v", result)
	}
}
