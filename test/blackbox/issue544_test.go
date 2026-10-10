package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue544MissingTableSchemaChangeErrorNumbers(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	mustQuery(t, client, "CREATE DATABASE app")
	mustQuery(t, client, "USE app")

	for _, tc := range []struct {
		query string
		code  uint16
	}{
		{"CREATE INDEX missing_idx ON no_such (id)", 1146},
		{"CREATE UNIQUE INDEX missing_uidx ON no_such (id)", 1146},
		{"ALTER TABLE no_such ADD COLUMN x INT NULL", 1146},
		{"ALTER TABLE no_such DROP COLUMN id", 1146},
		{"ALTER TABLE no_such RENAME TO other_name", 1146},
		{"DROP INDEX missing_idx ON no_such", 1146},
		{"DROP TABLE no_such", 1051},
	} {
		result := client.query(tc.query)
		if result.errCode != tc.code {
			t.Fatalf("%q: got code=%d, want %d (err: %s)", tc.query, result.errCode, tc.code, result.err)
		}
	}
}
