package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue220ShowColumnsDescribeAndStatus(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE devices (id INT PRIMARY KEY AUTO_INCREMENT, site VARCHAR(64) NOT NULL, reading DECIMAL(10,2) DEFAULT 0)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	describe := client.query("DESCRIBE devices")
	expectedColumns := []string{"Field", "Type", "Null", "Key", "Default", "Extra"}
	expectedRows := [][]string{
		{"id", "INT", "NO", "PRI", "", "auto_increment"},
		{"site", "VARCHAR(64)", "NO", "", "", ""},
		{"reading", "DECIMAL(10,2)", "YES", "", "0.00", ""},
	}
	if describe.err != "" || !equalStrings(describe.columns, expectedColumns) || !equalStringRows(describe.rows, expectedRows) {
		t.Fatalf("DESCRIBE devices: %#v", describe)
	}

	columns := client.query("SHOW COLUMNS FROM devices")
	if columns.err != "" || !equalStringRows(columns.rows, expectedRows) {
		t.Fatalf("SHOW COLUMNS FROM devices: %#v", columns)
	}

	full := client.query("SHOW FULL COLUMNS FROM devices")
	expectedFullColumns := []string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}
	if full.err != "" || !equalStrings(full.columns, expectedFullColumns) || len(full.rows) != 3 || full.rows[0][7] != "select,insert,update,references" {
		t.Fatalf("SHOW FULL COLUMNS FROM devices: %#v", full)
	}

	upper := client.query("SHOW COLUMNS IN app.devices LIKE 'rea%'")
	if upper.err != "" || len(upper.rows) != 1 || upper.rows[0][0] != "reading" {
		t.Fatalf("SHOW COLUMNS IN app.devices LIKE: %#v", upper)
	}

	status := client.query("SHOW STATUS")
	if status.err != "" || !equalStrings(status.columns, []string{"Variable_name", "Value"}) || len(status.rows) == 0 {
		t.Fatalf("SHOW STATUS: %#v", status)
	}
	sessionStatus := client.query("SHOW SESSION STATUS LIKE '%spill%'")
	if sessionStatus.err != "" || len(sessionStatus.rows) != 2 || sessionStatus.rows[0][0] != "spill_bytes" || sessionStatus.rows[1][0] != "spill_count" {
		t.Fatalf("SHOW SESSION STATUS LIKE: %#v", sessionStatus)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStringRows(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !equalStrings(left[index], right[index]) {
			return false
		}
	}
	return true
}
