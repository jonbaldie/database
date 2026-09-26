package blackbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestIssue448InsertReportsGeneratedLastInsertID proves that the OK packet of
// an INSERT carries the AUTO_INCREMENT value that database/sql reports as
// LastInsertId, through both the text and the binary protocol.
func TestIssue448InsertReportsGeneratedLastInsertID(t *testing.T) {
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
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"CREATE DATABASE trial",
		"CREATE TABLE trial.ids (id BIGINT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(20) NOT NULL)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	text := []struct {
		statement    string
		wantAffected int64
		wantID       int64
	}{
		{"INSERT INTO trial.ids (label) VALUES ('one')", 1, 1},
		{"INSERT INTO trial.ids (label) VALUES ('two'), ('three')", 2, 2},
		{"UPDATE trial.ids SET label = 'uno' WHERE id = 1", 1, 0},
		{"DELETE FROM trial.ids WHERE id = 3", 1, 0},
	}
	for _, tc := range text {
		result, err := db.ExecContext(ctx, tc.statement)
		if err != nil {
			t.Fatalf("text %s: %v", tc.statement, err)
		}
		checkIssue448Result(t, "text "+tc.statement, result, tc.wantAffected, tc.wantID)
	}

	prepared := []struct {
		statement string
		args      []any
		wantID    int64
	}{
		{"INSERT INTO trial.ids (label) VALUES (?)", []any{"four"}, 4},
		{"INSERT INTO trial.ids (id, label) VALUES (?, ?)", []any{nil, "five"}, 5},
	}
	for _, tc := range prepared {
		statement, err := db.PrepareContext(ctx, tc.statement)
		if err != nil {
			t.Fatalf("prepare %s: %v", tc.statement, err)
		}
		result, err := statement.ExecContext(ctx, tc.args...)
		_ = statement.Close()
		if err != nil {
			t.Fatalf("binary %s: %v", tc.statement, err)
		}
		checkIssue448Result(t, "binary "+tc.statement, result, 1, tc.wantID)
	}

	result, err := db.ExecContext(ctx, "CREATE TABLE trial.other (id INT PRIMARY KEY)")
	if err != nil {
		t.Fatal(err)
	}
	checkIssue448Result(t, "create table", result, 0, 0)
}

func checkIssue448Result(t *testing.T, label string, result sql.Result, wantAffected, wantID int64) {
	t.Helper()
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("%s: rows affected: %v", label, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("%s: last insert id: %v", label, err)
	}
	if affected != wantAffected || id != wantID {
		t.Fatalf("%s: rows affected %d, last insert id %d; want %d, %d", label, affected, id, wantAffected, wantID)
	}
}
