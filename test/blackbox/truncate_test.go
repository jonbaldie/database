package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLTruncateClearsPrimaryAndOrderedIndexes(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE records (id INT PRIMARY KEY, marker INT, INDEX idx_marker (marker))",
		"INSERT INTO records VALUES (1, 10)",
	} {
		mustQuery(t, client, query)
	}

	if result := client.query("SELECT id, marker FROM records FORCE INDEX (idx_marker)"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" || result.rows[0][1] != "10" {
		t.Fatalf("initial secondary-index scan: %#v", result)
	}
	mustQuery(t, client, "TRUNCATE TABLE records")
	if result := client.query("SELECT COUNT(*) FROM records"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "0" {
		t.Fatalf("truncate row count: %#v", result)
	}
	if result := client.query("INSERT INTO records VALUES (1, 20)"); result.err != "" {
		t.Fatalf("reinserted primary key after truncate: %#v", result)
	}
	if result := client.query("SELECT id, marker FROM records FORCE INDEX (idx_marker)"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" || result.rows[0][1] != "20" {
		t.Fatalf("secondary-index scan after truncate: %#v", result)
	}

	client.close()
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("stop after truncate: %#v", result)
	}

	process, address = startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	restarted := newWireClient(t, address, "admin", "lifecycle-secret")
	defer restarted.close()
	if result := restarted.query("USE app"); result.err != "" {
		t.Fatalf("select database after restart: %#v", result)
	}
	if result := restarted.query("SELECT id, marker FROM records FORCE INDEX (idx_marker)"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" || result.rows[0][1] != "20" {
		t.Fatalf("rows after restart: %#v", result)
	}
}
