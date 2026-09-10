package blackbox_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue363MixedCurrentTimeMetadata(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/?parseTime=true")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	cases := map[string]struct {
		typeName    string
		decimals    int64
		decimalOkay bool
	}{
		"SELECT NOW(), 1":        {typeName: "DATETIME", decimalOkay: true},
		"SELECT CURRENT_DATE, 1": {typeName: "DATE"},
		"SELECT CURRENT_TIME, 1": {typeName: "TIME", decimalOkay: true},
		"SELECT NOW(3), 1":       {typeName: "DATETIME", decimals: 3, decimalOkay: true},
	}
	for query, want := range cases {
		assertIssue363TemporalIntegerRows(t, query, want.typeName, want.decimals, want.decimalOkay, func() (*sql.Rows, error) {
			return db.QueryContext(ctx, query)
		})
	}

	for _, statement := range []string{
		"CREATE DATABASE issue363",
		"CREATE TABLE issue363.rows (id INT)",
		"INSERT INTO issue363.rows VALUES (1)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("execute %q: %v", statement, err)
		}
	}
	assertIssue363TemporalIntegerRows(t, "SELECT NOW(), id FROM issue363.rows", "DATETIME", 0, true, func() (*sql.Rows, error) {
		return db.QueryContext(ctx, "SELECT NOW(), id FROM issue363.rows")
	})

	prepared, err := db.PrepareContext(ctx, "SELECT NOW(3), 1")
	if err != nil {
		t.Fatalf("prepare current-time projection: %v", err)
	}
	assertIssue363TemporalIntegerRows(t, "prepared SELECT NOW(3), 1", "DATETIME", 3, true, func() (*sql.Rows, error) {
		return prepared.QueryContext(ctx)
	})
	if err := prepared.Close(); err != nil {
		t.Fatalf("close prepared current-time projection: %v", err)
	}
}

func assertIssue363TemporalIntegerRows(t *testing.T, label, wantType string, wantDecimals int64, wantDecimalOkay bool, query func() (*sql.Rows, error)) {
	t.Helper()
	rows, err := query()
	if err != nil {
		t.Fatalf("query %q: %v", label, err)
	}
	if !rows.Next() {
		rowErr := rows.Err()
		rows.Close()
		t.Fatalf("%q returned no row: %v", label, rowErr)
	}
	var temporal any
	var number int64
	if err := rows.Scan(&temporal, &number); err != nil {
		rows.Close()
		t.Fatalf("scan %q: %v", label, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatalf("column types for %q: %v", label, err)
	}
	if len(columns) != 2 {
		rows.Close()
		t.Fatalf("column types for %q = %d, want 2", label, len(columns))
	}
	if got := columns[0].DatabaseTypeName(); got != wantType {
		t.Errorf("%q database type = %q, want %q", label, got, wantType)
	}
	if got, _, ok := columns[0].DecimalSize(); ok != wantDecimalOkay || wantDecimalOkay && got != wantDecimals {
		t.Errorf("%q decimals = (%d, %t), want (%d, %t)", label, got, ok, wantDecimals, wantDecimalOkay)
	}
	if fmt.Sprint(temporal) == "" || number != 1 {
		t.Errorf("%q values = (%v, %d), want a temporal value and 1", label, temporal, number)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close %q: %v", label, err)
	}
}
