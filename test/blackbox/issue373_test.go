package blackbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue373DoubleQuotedStringLiterals(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := openIssue364Database(address)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var first, second string
	if err := db.QueryRowContext(ctx, `SELECT "it\"s", "it""s"`).Scan(&first, &second); err != nil {
		t.Fatalf("select double-quoted literals: %v", err)
	}
	if first != `it"s` || second != `it"s` {
		t.Fatalf("double-quoted literals = %q, %q, want %q, %q", first, second, `it"s`, `it"s`)
	}

	rows, err := db.QueryContext(ctx, `SELECT "abc"`)
	if err != nil {
		t.Fatalf(`query SELECT "abc": %v`, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatalf("column types: %v", err)
	}
	if got := columns[0].DatabaseTypeName(); got != "VARCHAR" {
		t.Errorf(`SELECT "abc" database type = %q, want VARCHAR`, got)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var ignored string
	err = db.QueryRowContext(ctx, `SELECT "abc`).Scan(&ignored)
	var failure *mysql.MySQLError
	if !errors.As(err, &failure) || failure.Number != 1064 {
		t.Fatalf(`SELECT "abc error = %v, want 1064`, err)
	}
}
