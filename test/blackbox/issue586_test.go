package blackbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue586PreparedLexicalRules(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		"CREATE DATABASE lexical", "USE lexical",
		"CREATE TABLE t (`column\\` INT)", "INSERT INTO t VALUES (7)",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, test := range []struct {
		query  string
		offset int
	}{
		{"SELECT ? /* ? */", 0}, {"SELECT ? # ?", 0}, {"SELECT ? -- ?\n", 0},
		{"SELECT `column\\` + ? FROM t", 7}, {"SELECT ? AS `a``?`", 0},
	} {
		query := test.query
		t.Run(query, func(t *testing.T) {
			statement, err := db.PrepareContext(ctx, query)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			defer statement.Close()
			for _, want := range []int{42, 91} {
				var got int
				// database/sql checks the server-advertised parameter count before execute.
				if err := statement.QueryRowContext(ctx, want).Scan(&got); err != nil {
					t.Fatalf("execute with one parameter: %v", err)
				}
				if got != want+test.offset {
					t.Fatalf("result = %d, want %d", got, want+test.offset)
				}
			}
		})
	}
}
