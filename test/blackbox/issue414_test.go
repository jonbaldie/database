package blackbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestIssue414TypeCoalescingFunctionsAdvertiseMergedType proves that IFNULL
// and COALESCE with mixed argument types advertise the merged column type, so
// both the text and the binary protocol return the row and keep the connection.
func TestIssue414TypeCoalescingFunctionsAdvertiseMergedType(t *testing.T) {
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
		"CREATE TABLE app.t (id INT PRIMARY KEY, n INT, d DOUBLE)",
		"INSERT INTO app.t VALUES (1, NULL, 2.5)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	cases := []struct {
		query    string
		want     string
		wantType string
	}{
		{"SELECT IFNULL(id, 'none') FROM app.t", "1", "VARCHAR"},
		{"SELECT IFNULL(n, 'none') FROM app.t", "none", "VARCHAR"},
		{"SELECT IFNULL(MAX(id), 'none') FROM app.t WHERE 1=0", "none", "VARCHAR"},
		{"SELECT COALESCE(n, 'none') FROM app.t", "none", "VARCHAR"},
		{"SELECT COALESCE(n, id, 'none') FROM app.t", "1", "VARCHAR"},
		{"SELECT COALESCE(MAX(id), 'none') FROM app.t WHERE 1=0", "none", "VARCHAR"},
		{"SELECT IFNULL(n, d) FROM app.t", "2.5", "DOUBLE"},
		{"SELECT COALESCE(n, d) FROM app.t", "2.5", "DOUBLE"},
		{"SELECT IFNULL(id, n) FROM app.t", "1", "BIGINT"},
	}
	for _, tc := range cases {
		rows, err := db.QueryContext(ctx, tc.query)
		if err != nil {
			t.Fatalf("text %s: %v", tc.query, err)
		}
		checkIssue414Rows(t, "text "+tc.query, rows, tc.want, tc.wantType)

		prepared, err := db.PrepareContext(ctx, tc.query)
		if err != nil {
			t.Fatalf("prepare %s: %v", tc.query, err)
		}
		rows, err = prepared.QueryContext(ctx)
		if err != nil {
			t.Fatalf("binary %s: %v", tc.query, err)
		}
		checkIssue414Rows(t, "binary "+tc.query, rows, tc.want, tc.wantType)
		_ = prepared.Close()
	}
}

func checkIssue414Rows(t *testing.T, label string, rows *sql.Rows, want, wantType string) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("%s column types: %v", label, err)
	}
	if got := columns[0].DatabaseTypeName(); got != wantType {
		t.Errorf("%s type = %s, want %s", label, got, wantType)
	}
	if !rows.Next() {
		t.Fatalf("%s returned no row: %v", label, rows.Err())
	}
	var got string
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("%s scan: %v", label, err)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", label, got, want)
	}
	if rows.Next() {
		t.Errorf("%s returned more than one row", label)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s rows: %v", label, err)
	}
}
