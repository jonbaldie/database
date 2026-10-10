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

func TestIssue542InformationSchemaOrdinalSortsNumerically(t *testing.T) {
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
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"CREATE DATABASE app",
		"CREATE TABLE app.wide (c01 INT, c02 INT, c03 INT, c04 INT, c05 INT, c06 INT, c07 INT, c08 INT, c09 INT, c10 INT, c11 INT, c12 INT)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("execute %q: %v", statement, err)
		}
	}

	query := "SELECT COLUMN_NAME, ORDINAL_POSITION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 'wide' ORDER BY ORDINAL_POSITION"
	want := []string{"c01", "c02", "c03", "c04", "c05", "c06", "c07", "c08", "c09", "c10", "c11", "c12"}
	for name, run := range map[string]func() (*sql.Rows, error){
		"text":     func() (*sql.Rows, error) { return db.QueryContext(ctx, query) },
		"prepared": func() (*sql.Rows, error) { return db.QueryContext(ctx, query+" + ?", 0) },
	} {
		rows, err := run()
		if err != nil {
			t.Fatalf("%s query: %v", name, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || types[1].DatabaseTypeName() != "INT" {
			t.Fatalf("%s ORDINAL_POSITION type = %v, %v; want INT", name, types, err)
		}
		var names []string
		for rows.Next() {
			var column string
			var ordinal int64
			if err := rows.Scan(&column, &ordinal); err != nil {
				t.Fatalf("%s scan: %v", name, err)
			}
			if ordinal != int64(len(names)+1) {
				t.Fatalf("%s %s ordinal = %d", name, column, ordinal)
			}
			names = append(names, column)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s rows: %v", name, err)
		}
		_ = rows.Close()
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("%s order = %v, want %v", name, names, want)
		}
	}
}
