package blackbox_test

import (
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestIssue460RowLocksUseCanonicalTableIdentity proves that a row lock taken
// through one SQL identifier spelling excludes a lock or write through every
// equivalent spelling of the same table.
func TestIssue460RowLocksUseCanonicalTableIdentity(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "lock-test-secret")
	process, address := startMySQLServer(t, runner, directory, "--lock-wait-timeout-ms=500")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", "lock-test-secret")
	defer admin.close()
	mustQuery(t, admin, "CREATE DATABASE coordination")
	mustQuery(t, admin, "USE coordination")
	mustQuery(t, admin, "CREATE TABLE work (id INT PRIMARY KEY, value INT)")
	mustQuery(t, admin, "INSERT INTO work VALUES (1, 10), (2, 10), (3, 10), (4, 10), (5, 10)")
	mustQuery(t, admin, "CREATE TABLE `café` (id INT PRIMARY KEY, value INT)")
	mustQuery(t, admin, "INSERT INTO `café` VALUES (1, 10)")

	cases := []struct {
		name       string
		ownerUse   string
		ownerLock  string
		probeUse   string
		probeQuery string
		wantCode   uint16
	}{
		{"table case", "coordination", "SELECT id FROM Work WHERE id = 1 FOR UPDATE", "coordination", "SELECT id FROM work WHERE id = 1 FOR UPDATE NOWAIT", 3572},
		{"write through table case", "coordination", "SELECT id FROM Work WHERE id = 2 FOR UPDATE", "coordination", "UPDATE work SET value = 20 WHERE id = 2", 1205},
		{"session namespace case", "COORDINATION", "SELECT id FROM work WHERE id = 3 FOR UPDATE", "coordination", "SELECT id FROM work WHERE id = 3 FOR UPDATE NOWAIT", 3572},
		{"qualified namespace case", "coordination", "SELECT id FROM Coordination.WORK WHERE id = 4 FOR UPDATE", "coordination", "DELETE FROM coordination.work WHERE id = 4", 1205},
		{"write lock then locking read", "coordination", "UPDATE WORK SET value = 50 WHERE id = 5", "coordination", "SELECT id FROM work WHERE id = 5 FOR SHARE NOWAIT", 3572},
		{"canonical equivalence", "coordination", "SELECT id FROM `café` WHERE id = 1 FOR UPDATE", "coordination", "SELECT id FROM `CAFÉ` WHERE id = 1 FOR UPDATE NOWAIT", 3572},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := newWireClient(t, address, "admin", "lock-test-secret")
			defer owner.close()
			probe := newWireClient(t, address, "admin", "lock-test-secret")
			defer probe.close()
			mustQuery(t, owner, "USE "+tc.ownerUse)
			mustQuery(t, probe, "USE "+tc.probeUse)
			mustQuery(t, owner, "BEGIN")
			mustQuery(t, owner, tc.ownerLock)
			mustQuery(t, probe, "BEGIN")
			if result := probe.query(tc.probeQuery); result.errCode != tc.wantCode {
				t.Fatalf("%s while %q holds the row: %#v", tc.probeQuery, tc.ownerLock, result)
			}
			mustQuery(t, probe, "ROLLBACK")
			mustQuery(t, owner, "ROLLBACK")
		})
	}
}
