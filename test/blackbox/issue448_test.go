package blackbox_test

import (
	"database/sql"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue448LastInsertID(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"CREATE DATABASE trial", "CREATE TABLE trial.ids (id BIGINT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(20) NOT NULL)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		args  []any
		id    int64
	}{
		{"INSERT INTO trial.ids (label) VALUES ('one')", nil, 1},
		{"INSERT INTO trial.ids (label) VALUES ('two'), ('three')", nil, 2},
		{"INSERT INTO trial.ids (id, label) VALUES (10, 'explicit')", nil, 0},
		{"INSERT INTO trial.ids (label) VALUES (?)", []any{"prepared"}, 11},
		{"INSERT INTO trial.ids (id, label) VALUES (?, ?)", []any{nil, "null"}, 12},
		{"INSERT INTO trial.ids (id, label) VALUES (300, 'explicit2'), (NULL, 'mixed')", nil, 301},
	} {
		result, err := db.Exec(tc.query, tc.args...)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		id, err := result.LastInsertId()
		if err != nil || id != tc.id {
			t.Errorf("%s: LastInsertId = %d, %v; want %d", tc.query, id, err, tc.id)
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM trial.ids").Scan(&count); err != nil || count != 8 {
		t.Fatalf("stored row count = %d, %v; want 8", count, err)
	}
}
