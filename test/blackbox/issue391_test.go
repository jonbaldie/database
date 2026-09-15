package blackbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

// TestIssue391TruncatedStatementsKeepServerAlive proves that statements which
// end immediately after a target keyword, or with a trailing dotted part,
// return a clean syntax error instead of ending the server process.
func TestIssue391TruncatedStatementsKeepServerAlive(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openIssue364Database(address)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	statements := []string{
		"SELECT FROM",
		"SELECT * FROM",
		"SELECT 1 FROM",
		"SELECT a FROM ",
		"SELECT 1 UNION SELECT FROM",
		"SELECT 1 FROM `t` .",
		"SELECT 1 FROM `t`.",
		"SELECT FROM WHERE 1",
		"SELECT 1 FROM t.",
		"SELECT 1 FROM t..u",
		"SELECT 1 FROM a.b.c",
		"DELETE FROM ",
		"INSERT INTO ",
		"UPDATE ",
		"UPDATE `t` .",
		"CREATE TABLE ",
		"CREATE TABLE `t` .",
		"DROP TABLE ",
		"TRUNCATE TABLE ",
		"ALTER TABLE ",
		"ALTER TABLE `t` .",
		"RENAME TABLE ",
		"REPLACE INTO ",
	}
	for _, statement := range statements {
		_, err := db.ExecContext(ctx, statement)
		if err == nil {
			t.Errorf("%q = no error, want a client error", statement)
			continue
		}
		var failure *mysql.MySQLError
		if !errors.As(err, &failure) {
			t.Errorf("%q error = %v, want a MySQL protocol error", statement, err)
		}
	}

	for _, statement := range []string{"SELECT FROM", "SELECT 1 FROM `t` ."} {
		prepared, err := db.PrepareContext(ctx, statement)
		if err == nil {
			_, err = prepared.ExecContext(ctx)
			_ = prepared.Close()
		}
		if err == nil {
			t.Errorf("prepared %q = no error, want a client error", statement)
			continue
		}
		var failure *mysql.MySQLError
		if !errors.As(err, &failure) {
			t.Errorf("prepared %q error = %v, want a MySQL protocol error", statement, err)
		}
	}

	var alive int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&alive); err != nil {
		t.Fatalf("server did not survive the truncated statements: %v", err)
	}
	if alive != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", alive)
	}
}
