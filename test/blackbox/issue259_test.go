package blackbox_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestIssue259DropIndexedColumn covers ALTER TABLE ... DROP COLUMN on columns
// that belong to a secondary index, a UNIQUE constraint, and the primary key.
// MySQL removes the dropped column from each key and drops the key once none
// of its parts remain, so the ALTER succeeds and the table stays usable.
func TestIssue259DropIndexedColumn(t *testing.T) {
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
		"CREATE TABLE members (id INT PRIMARY KEY, email VARCHAR(40) UNIQUE, name VARCHAR(20), age INT, INDEX(name), INDEX age_name (age, name))",
		"INSERT INTO members VALUES (1, 'ada@x.test', 'Ada', 30), (2, 'bob@x.test', 'Bob', 25)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	if result := client.query("ALTER TABLE members DROP COLUMN name"); result.err != "" {
		t.Fatalf("DROP COLUMN on an indexed column: %#v", result)
	}

	// The pruned schema and the narrowed rows must survive a restart.
	client.close()
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("stop after DROP COLUMN: %#v", result)
	}
	process, address = startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	client = newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	if result := client.query("USE app"); result.err != "" {
		t.Fatalf("select database after restart: %#v", result)
	}

	result := client.query("SELECT id, email, age FROM members ORDER BY id")
	if result.err != "" {
		t.Fatalf("SELECT after DROP COLUMN: %#v", result)
	}
	want := [][]string{{"1", "ada@x.test", "30"}, {"2", "bob@x.test", "25"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows after DROP COLUMN = %#v, want %#v", result.rows, want)
	}
	if result := client.query("SHOW CREATE TABLE members"); result.err != "" {
		t.Fatalf("SHOW CREATE TABLE after DROP COLUMN: %#v", result)
	} else if strings.Contains(result.rows[0][1], "`name`") {
		t.Fatalf("SHOW CREATE TABLE still references the dropped column: %#v", result.rows[0][1])
	}

	// The surviving secondary index still answers an indexed scan, and the
	// surviving UNIQUE constraint still rejects a duplicate.
	if result := client.query("SELECT id, age FROM members FORCE INDEX (age_name) WHERE age = 30"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("surviving composite index scan: %#v", result)
	}
	if result := client.query("INSERT INTO members VALUES (3, 'ada@x.test', 40)"); result.err == "" {
		t.Fatalf("duplicate email accepted after DROP COLUMN")
	}
}
