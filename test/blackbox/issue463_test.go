package blackbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue463TypedComparisonAcrossSubqueryBoundaries(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE DATABASE issue463",
		"CREATE TABLE issue463.d (id INT PRIMARY KEY, v DATE)",
		"CREATE TABLE issue463.dt (id INT PRIMARY KEY, v DATETIME)",
		"CREATE TABLE issue463.b (id INT PRIMARY KEY, v VARCHAR(10) COLLATE utf8mb4_bin)",
		"INSERT INTO issue463.d VALUES (1, '2020-01-01')",
		"INSERT INTO issue463.dt VALUES (1, '2020-01-01 00:00:00')",
		"INSERT INTO issue463.b VALUES (1, 'a')",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("execute %q: %v", statement, err)
		}
	}
	for query, want := range map[string]int{
		"SELECT d.id FROM issue463.d AS d JOIN issue463.dt AS dt ON d.v = dt.v": 1,
		"SELECT id FROM issue463.d WHERE v = (SELECT v FROM issue463.dt)":       1,
		"SELECT id FROM issue463.d WHERE v IN (SELECT v FROM issue463.dt)":      1,
		"SELECT id FROM issue463.b WHERE v = 'A'":                               0,
		"SELECT id FROM issue463.b WHERE 'A' = (SELECT v FROM issue463.b)":      0,
		"SELECT id FROM issue463.b WHERE 'a' = (SELECT v FROM issue463.b)":      1,
	} {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		count := 0
		for rows.Next() {
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows %q: %v", query, err)
		}
		_ = rows.Close()
		if count != want {
			t.Fatalf("%s returned %d rows, want %d", query, count, want)
		}
	}
}
