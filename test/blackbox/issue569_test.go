package blackbox_test

import (
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue569LiveExplanationRespectsObservation(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "observation-secret")
	process, address := startMySQLServer(t, runner, directory, "--lock-wait-timeout-ms=10000")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "observation-secret")
	defer admin.close()
	for _, query := range []string{
		"CREATE DATABASE observation",
		"USE observation",
		"CREATE TABLE records (id INT PRIMARY KEY, value INT)",
		"INSERT INTO records VALUES (1, 10)",
		"CREATE USER 'peek' IDENTIFIED BY 'peek-observation-secret'",
		"CREATE USER 'worker' IDENTIFIED BY 'worker-observation-secret'",
		"GRANT DATA_READ ON observation.* TO 'worker'",
		"GRANT DATA_WRITE ON observation.* TO 'worker'",
		"CREATE USER 'auditor' IDENTIFIED BY 'auditor-observation-secret'",
		"GRANT OPERATIONAL_OBSERVATION ON *.* TO 'auditor'",
	} {
		mustQuery(t, admin, query)
	}
	owner := newWireClient(t, address, "admin", "observation-secret")
	defer owner.close()
	writer := newWireClient(t, address, "admin", "observation-secret")
	defer writer.close()
	peek := newWireClient(t, address, "peek", "peek-observation-secret")
	defer peek.close()
	auditor := newWireClient(t, address, "auditor", "auditor-observation-secret")
	defer auditor.close()
	worker := newWireClient(t, address, "worker", "worker-observation-secret")
	defer worker.close()
	workerObserver := newWireClient(t, address, "worker", "worker-observation-secret")
	defer workerObserver.close()
	for _, client := range []*wireClient{owner, writer} {
		mustQuery(t, client, "USE observation")
		mustQuery(t, client, "BEGIN")
	}
	mustQuery(t, owner, "SELECT id FROM records WHERE id = 1 FOR UPDATE")
	blocked := queryAsync(writer, "UPDATE records SET value = 20 WHERE id = 1")
	waitForProcessListQuery(t, admin, writer.connectionID)
	waitForLiveLockWait(t, admin, writer.connectionID)

	for _, observer := range []*wireClient{admin, peek, auditor, workerObserver} {
		show := processIDs(t, observer.query("SHOW PROCESSLIST"))
		informationSchema := processIDs(t, observer.query("SELECT ID FROM information_schema.PROCESSLIST"))
		if !reflect.DeepEqual(show, informationSchema) {
			t.Fatalf("process list visibility differs: SHOW=%v information_schema=%v", show, informationSchema)
		}
	}
	if processListHasConnection(peek.query("SHOW PROCESSLIST"), writer.connectionID) {
		t.Fatal("unprivileged process list exposed the admin session")
	}
	liveQueryExplanation(t, auditor, writer.connectionID)
	id := strconv.FormatUint(uint64(writer.connectionID), 10)
	for _, prefix := range []string{"EXPLAIN FOR CONNECTION ", "EXPLAIN FORMAT=JSON FOR CONNECTION "} {
		result := peek.query(prefix + id)
		if result.errCode != 1094 || len(result.rows) != 0 || strings.Contains(result.err, "UPDATE records") {
			t.Fatalf("unprivileged live explanation exposed the admin session: %#v", result)
		}
		unknown := peek.query(prefix + "4294967295")
		if unknown.errCode != result.errCode || unknown.errState != result.errState || unknown.err != result.err {
			t.Fatalf("hidden session error differs from unknown session: hidden=%#v unknown=%#v", result, unknown)
		}
	}
	mustQuery(t, owner, "COMMIT")
	mustQueryResult(t, <-blocked, "blocked admin write")
	mustQuery(t, writer, "COMMIT")

	mustQuery(t, owner, "BEGIN")
	mustQuery(t, owner, "SELECT id FROM records WHERE id = 1 FOR UPDATE")
	mustQuery(t, worker, "USE observation")
	mustQuery(t, worker, "BEGIN")
	workerBlocked := queryAsync(worker, "UPDATE records SET value = 30 WHERE id = 1")
	waitForProcessListQuery(t, admin, worker.connectionID)
	waitForLiveLockWait(t, admin, worker.connectionID)
	liveQueryExplanation(t, workerObserver, worker.connectionID)
	traditional := workerObserver.query("EXPLAIN FOR CONNECTION " + strconv.FormatUint(uint64(worker.connectionID), 10))
	if traditional.err != "" || len(traditional.rows) == 0 {
		t.Fatalf("same-account traditional explanation: %#v", traditional)
	}
	mustQuery(t, owner, "COMMIT")
	mustQueryResult(t, <-workerBlocked, "blocked worker write")
	mustQuery(t, worker, "COMMIT")
}

func processIDs(t *testing.T, result wireResult) []string {
	t.Helper()
	if result.err != "" {
		t.Fatalf("process list query: %#v", result)
	}
	ids := make([]string, 0, len(result.rows))
	for _, row := range result.rows {
		ids = append(ids, row[0])
	}
	sort.Strings(ids)
	return ids
}
