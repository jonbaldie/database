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

func TestIssue364StringFunctionsPreserveBinarySemantics(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := openIssue364Database(address)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	for _, tc := range []struct {
		query string
		want  int64
	}{
		{query: "SELECT CONCAT(CAST('a' AS BINARY), '') = 'A'", want: 0},
		{query: "SELECT CONCAT(CAST('a' AS BINARY), '') LIKE 'A'", want: 0},
		{query: "SELECT SUBSTRING(CAST('a' AS BINARY), 1) = 'A'", want: 0},
		{query: "SELECT CONCAT('', CAST('a' AS BINARY)) = 'A'", want: 0},
		{query: "SELECT CONCAT('a', '') = 'A'", want: 1},
		{query: "SELECT SUBSTRING('a', 1) = 'A'", want: 1},
	} {
		var result int64
		if err := db.QueryRowContext(ctx, tc.query).Scan(&result); err != nil {
			t.Fatalf("query %q: %v", tc.query, err)
		}
		if result != tc.want {
			t.Errorf("query %q = %d, want %d", tc.query, result, tc.want)
		}
	}

	for _, tc := range []struct {
		query        string
		wantType     string
		wantScanType reflect.Type
	}{
		{
			query:        "SELECT CONCAT(CAST('a' AS BINARY), '')",
			wantType:     "VARBINARY",
			wantScanType: reflect.TypeOf([]byte(nil)),
		},
		{
			query:        "SELECT SUBSTRING(CAST('a' AS BINARY), 1)",
			wantType:     "VARBINARY",
			wantScanType: reflect.TypeOf([]byte(nil)),
		},
		{
			query:        "SELECT CONCAT('a', '')",
			wantType:     "VARCHAR",
			wantScanType: reflect.TypeOf(""),
		},
		{
			query:        "SELECT SUBSTRING('a', 1)",
			wantType:     "VARCHAR",
			wantScanType: reflect.TypeOf(""),
		},
	} {
		rows, err := db.QueryContext(ctx, tc.query)
		if err != nil {
			t.Fatalf("query %q: %v", tc.query, err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatalf("query %q returned no row: %v", tc.query, rows.Err())
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatalf("column types for %q: %v", tc.query, err)
		}
		if len(columns) != 1 {
			rows.Close()
			t.Fatalf("column types for %q = %d, want 1", tc.query, len(columns))
		}
		if got := columns[0].DatabaseTypeName(); got != tc.wantType {
			t.Errorf("%q database type = %q, want %q", tc.query, got, tc.wantType)
		}
		if got := columns[0].ScanType(); got != tc.wantScanType {
			t.Errorf("%q scan type = %v, want %v", tc.query, got, tc.wantScanType)
		}
		var value []byte
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			t.Fatalf("scan %q: %v", tc.query, err)
		}
		if string(value) != "a" {
			t.Errorf("%q value = %q, want %q", tc.query, value, "a")
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close %q: %v", tc.query, err)
		}
	}
}

func openIssue364Database(address string) (*sql.DB, error) {
	return sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
}
