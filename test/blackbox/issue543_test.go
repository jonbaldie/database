package blackbox_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue543ContractedMetadataThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		"CREATE DATABASE app",
		"CREATE TABLE app.p (id INT PRIMARY KEY AUTO_INCREMENT, code INT, label VARCHAR(12) NOT NULL DEFAULT 'hello', UNIQUE (code))",
		"INSERT INTO app.p (code) VALUES (7), (8)",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	for _, prepared := range []bool{false, true} {
		query := "SELECT ENGINE, TABLE_ROWS, AUTO_INCREMENT, ROW_FORMAT, DATA_LENGTH, CREATE_TIME, CREATE_OPTIONS, TABLE_COLLATION, TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 'p'"
		var args []any
		if prepared {
			query = "SELECT ENGINE, TABLE_ROWS, AUTO_INCREMENT, ROW_FORMAT, DATA_LENGTH, CREATE_TIME, CREATE_OPTIONS, TABLE_COLLATION, TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?"
			args = []any{"app", "p"}
		}
		var engine, options, collation, comment string
		var count, next uint64
		var format, length, created sql.NullString
		err := db.QueryRowContext(ctx, query, args...).Scan(&engine, &count, &next, &format, &length, &created, &options, &collation, &comment)
		if err != nil || engine != "DATABASE" || count != 2 || next != 3 || format.Valid || length.Valid || created.Valid || options != "" || collation != "utf8mb4_0900_ai_ci" || comment != "" {
			t.Fatalf("prepared=%v table facts: engine=%s rows=%d next=%d physical=%v/%v/%v options=%q collation=%s comment=%q err=%v", prepared, engine, count, next, format, length, created, options, collation, comment, err)
		}
		t.Logf("prepared=%v ENGINE=%s TABLE_ROWS=%d AUTO_INCREMENT=%d; physical facts are NULL", prepared, engine, count, next)
	}

	for _, prepared := range []bool{false, true} {
		query := "SELECT COLUMN_NAME, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 'p' ORDER BY ORDINAL_POSITION"
		var args []any
		if prepared {
			query = "SELECT COLUMN_NAME, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION"
			args = []any{"app", "p"}
		}
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var got [][]string
		for rows.Next() {
			var name, nullable, key, extra string
			var defaultValue sql.NullString
			if err := rows.Scan(&name, &nullable, &defaultValue, &key, &extra); err != nil {
				t.Fatal(err)
			}
			value := "NULL"
			if defaultValue.Valid {
				value = defaultValue.String
			}
			got = append(got, []string{name, nullable, value, key, extra})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		want := [][]string{{"id", "NO", "NULL", "PRI", "auto_increment"}, {"code", "YES", "NULL", "MUL", ""}, {"label", "NO", "hello", "", ""}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prepared=%v column facts = %#v, want %#v", prepared, got, want)
		}
		t.Logf("prepared=%v column facts: %v", prepared, got)
	}

	for _, query := range []string{
		"SELECT TABLE_TYPE, TABLE_ROWS FROM information_schema.TABLES LIMIT 0",
		"SELECT COLUMN_KEY, NUMERIC_PRECISION FROM information_schema.COLUMNS LIMIT 0",
	} {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		_ = rows.Close()
		if err != nil || len(types) != 2 || (types[0].DatabaseTypeName() != "ENUM" || types[1].DatabaseTypeName() != "UNSIGNED BIGINT") {
			t.Fatalf("%s types = %v, %v", query, types, err)
		}
		for index, want := range []bool{false, true} {
			nullable, known := types[index].Nullable()
			if !known || nullable != want {
				t.Fatalf("%s column %d nullable=%v known=%v; want %v", query, index, nullable, known, want)
			}
		}
	}
}
