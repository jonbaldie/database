package mysql

import (
	"errors"
	"testing"
)

// TestIssue544MissingTableSchemaChangeErrorNumbers checks that CREATE INDEX,
// ALTER TABLE, and DROP INDEX on a missing table return MySQL error 1146 and
// SQLSTATE 42S02, while DROP TABLE keeps 1051.
func TestIssue544MissingTableSchemaChangeErrorNumbers(t *testing.T) {
	for _, tc := range []struct {
		query   string
		code    uint16
		state   string
		message string
	}{
		{"CREATE INDEX missing_idx ON no_such (id)", 1146, "42S02", "table does not exist"},
		{"CREATE UNIQUE INDEX missing_uidx ON no_such (id)", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE no_such ADD COLUMN x INT NULL", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE no_such DROP COLUMN id", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE no_such RENAME TO other_name", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE no_such ADD INDEX idx (id)", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE no_such DROP INDEX idx", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE no_such ALTER INDEX idx INVISIBLE", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE no_such MODIFY COLUMN id BIGINT", 1146, "42S02", "table does not exist"},
		{"RENAME TABLE no_such TO other_name", 1146, "42S02", "table does not exist"},
		{"TRUNCATE TABLE no_such", 1146, "42S02", "table does not exist"},
		{"DROP INDEX missing_idx ON no_such", 1146, "42S02", "table does not exist"},
		{"DROP TABLE no_such", 1051, "42S02", "table does not exist"},
		{"CREATE INDEX missing_idx ON app.no_such (id)", 1146, "42S02", "table does not exist"},
		{"ALTER TABLE app.no_such ADD COLUMN x INT NULL", 1146, "42S02", "table does not exist"},
		{"DROP INDEX missing_idx ON app.no_such", 1146, "42S02", "table does not exist"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			_, err := executeStatement(executor, tc.query)
			var failure sqlFailure
			if !errors.As(err, &failure) {
				t.Fatalf("error = %v, want SQL failure %d", err, tc.code)
			}
			if failure.code != tc.code || failure.state != tc.state || failure.message != tc.message {
				t.Fatalf("got code=%d state=%s message=%q, want code=%d state=%s message=%q", failure.code, failure.state, failure.message, tc.code, tc.state, tc.message)
			}
		})
	}
}
