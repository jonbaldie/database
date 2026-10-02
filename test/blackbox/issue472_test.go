package blackbox_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue472IFMixedTypeBinaryProtocol(t *testing.T) {
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
		"CREATE TABLE app.t (id INT PRIMARY KEY, d DOUBLE)",
		"INSERT INTO app.t VALUES (1, 2.5), (2, 0.5)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	cases := []struct {
		query    string
		want     []string
		wantType string
	}{
		{"SELECT IF(id = 1, id, 'none') FROM app.t ORDER BY id", []string{"1", "none"}, "VARCHAR"},
		{"SELECT IF(id = 2, id, 'none') FROM app.t ORDER BY id", []string{"none", "2"}, "VARCHAR"},
		{"SELECT IF(id = 1, id, d) FROM app.t ORDER BY id", []string{"1", "0.5"}, "DOUBLE"},
		{"SELECT IF(id = 2, id, d) FROM app.t ORDER BY id", []string{"2.5", "2"}, "DOUBLE"},
	}
	for _, tc := range cases {
		rows, err := db.QueryContext(ctx, tc.query)
		if err != nil {
			t.Errorf("text %s: %v", tc.query, err)
		} else {
			checkIssue472Rows(t, "text "+tc.query, rows, tc.want, tc.wantType)
		}

		prepared, err := db.PrepareContext(ctx, tc.query)
		if err != nil {
			t.Errorf("prepare %s: %v", tc.query, err)
			continue
		}
		rows, err = prepared.QueryContext(ctx)
		if err != nil {
			t.Errorf("binary %s: %v", tc.query, err)
		} else {
			checkIssue472Rows(t, "binary "+tc.query, rows, tc.want, tc.wantType)
		}
		_ = prepared.Close()
	}
}

func checkIssue472Rows(t *testing.T, label string, rows *sql.Rows, want []string, wantType string) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Errorf("%s column types: %v", label, err)
		return
	}
	if len(columns) != 1 {
		t.Errorf("%s column count = %d, want 1", label, len(columns))
		return
	}
	if got := columns[0].DatabaseTypeName(); got != wantType {
		t.Errorf("%s type = %s, want %s", label, got, wantType)
	}
	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Errorf("%s scan: %v", label, err)
			break
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Errorf("%s rows: %v", label, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s rows = %#v, want %#v", label, got, want)
	}
}
