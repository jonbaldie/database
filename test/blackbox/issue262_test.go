package blackbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue262StringLiteralEscapesThroughMySQLAndPreparedValues(t *testing.T) {
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
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"CREATE DATABASE issue262",
		"USE issue262",
		"CREATE TABLE strings (id INT PRIMARY KEY, value VARCHAR(64))",
		`INSERT INTO strings VALUES
			(1, 'a\\b'),
			(2, 'line1\nline2'),
			(3, 'a\0b'),
			(4, 'it\'s'),
			(5, 'a\"b'),
			(6, 'a\rb'),
			(7, 'a\tb'),
			(8, 'a\bb'),
			(9, 'a\Zb')`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	rows, err := db.QueryContext(ctx, "SELECT id, value, LENGTH(value), CHAR_LENGTH(value) FROM strings ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []struct {
		id              int
		value           string
		length          int
		characterLength int
	}{
		{1, "a\\b", 3, 3},
		{2, "line1\nline2", 11, 11},
		{3, "a\x00b", 3, 3},
		{4, "it's", 4, 4},
		{5, "a\"b", 3, 3},
		{6, "a\rb", 3, 3},
		{7, "a\tb", 3, 3},
		{8, "a\bb", 3, 3},
		{9, "a\x1ab", 3, 3},
	}
	for index, expected := range want {
		if !rows.Next() {
			t.Fatalf("row %d missing: %v", index, rows.Err())
		}
		var got struct {
			id              int
			value           string
			length          int
			characterLength int
		}
		if err := rows.Scan(&got.id, &got.value, &got.length, &got.characterLength); err != nil {
			t.Fatal(err)
		}
		if got.id != expected.id || got.value != expected.value || got.length != expected.length || got.characterLength != expected.characterLength {
			t.Fatalf("row %d = %#v, want %#v", index, got, expected)
		}
	}
	if rows.Next() {
		t.Fatal("query returned more rows than expected")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	statement, err := db.PrepareContext(ctx, "INSERT INTO strings VALUES (?, ?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := statement.ExecContext(ctx, 10, `a\b`); err != nil {
		_ = statement.Close()
		t.Fatal(err)
	}
	if err := statement.Close(); err != nil {
		t.Fatal(err)
	}
	var preparedValue string
	var preparedLength int
	if err := db.QueryRowContext(ctx, "SELECT value, LENGTH(value) FROM strings WHERE id = ?", 10).Scan(&preparedValue, &preparedLength); err != nil {
		t.Fatal(err)
	}
	if preparedValue != `a\b` || preparedLength != 3 {
		t.Fatalf("prepared value = %q, length = %d, want %q, length 3", preparedValue, preparedLength, `a\b`)
	}
}
