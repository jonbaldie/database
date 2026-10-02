package blackbox_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue473MixedNumericExtremesBinaryProtocol(t *testing.T) {
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
	for _, statement := range []string{
		"CREATE DATABASE app",
		"CREATE TABLE app.least_values (id INT PRIMARY KEY, d DOUBLE)",
		"INSERT INTO app.least_values VALUES (1, 2.5), (2, 0.5)",
		"CREATE TABLE app.greatest_values (id INT PRIMARY KEY, d DOUBLE)",
		"INSERT INTO app.greatest_values VALUES (1, 0.5), (2, 2.5)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"LEAST", "SELECT LEAST(id, d) FROM app.least_values ORDER BY id", []string{"1", "0.5"}},
		{"LEASTReverseOrder", "SELECT LEAST(id, d) FROM app.least_values ORDER BY id DESC", []string{"0.5", "1"}},
		{"GREATEST", "SELECT GREATEST(id, d) FROM app.greatest_values ORDER BY id", []string{"1", "2.5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			textRows, err := db.QueryContext(ctx, tc.query)
			if err != nil {
				t.Fatalf("text %s: %v", tc.query, err)
			}
			checkIssue473Rows(t, "text "+tc.query, textRows, tc.want)

			prepared, err := db.PrepareContext(ctx, tc.query)
			if err != nil {
				t.Fatalf("prepare %s: %v", tc.query, err)
			}
			rows, err := prepared.QueryContext(ctx)
			if err != nil {
				t.Fatalf("binary %s: %v", tc.query, err)
			}
			checkIssue473Rows(t, tc.query, rows, tc.want)
			_ = prepared.Close()
		})
	}
}

func checkIssue473Rows(t *testing.T, query string, rows *sql.Rows, want []string) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Errorf("%s column types: %v", query, err)
		return
	}
	if len(columns) != 1 {
		t.Errorf("%s column count = %d, want 1", query, len(columns))
		return
	}
	if got := columns[0].DatabaseTypeName(); got != "DOUBLE" {
		t.Errorf("%s type = %s, want DOUBLE", query, got)
	}
	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Errorf("%s scan: %v", query, err)
			break
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Errorf("%s rows: %v", query, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s rows = %#v, want %#v", query, got, want)
	}
}
