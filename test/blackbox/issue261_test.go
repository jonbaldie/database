package blackbox_test

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue261PreparedBinaryResultTypes(t *testing.T) {
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
		"CREATE DATABASE issue261",
		"USE issue261",
		"CREATE TABLE values_for_binary_results (tiny_value TINYINT NOT NULL, tiny_unsigned_value TINYINT UNSIGNED NOT NULL, small_value SMALLINT NOT NULL, small_unsigned_value SMALLINT UNSIGNED NOT NULL, medium_value MEDIUMINT NOT NULL, medium_unsigned_value MEDIUMINT UNSIGNED NOT NULL, float_value FLOAT NOT NULL, bit_value BIT(8) NOT NULL)",
		"INSERT INTO values_for_binary_results VALUES (42, 255, 32000, 65535, 8388607, 16777215, 1.25, 10)",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	var tiny int8
	queryPreparedValue(t, db, ctx, "SELECT tiny_value FROM values_for_binary_results", &tiny)
	if tiny != 42 {
		t.Fatalf("prepared TINYINT = %d, want 42", tiny)
	}

	var tinyUnsigned uint8
	queryPreparedValue(t, db, ctx, "SELECT tiny_unsigned_value FROM values_for_binary_results", &tinyUnsigned)
	if tinyUnsigned != 255 {
		t.Fatalf("prepared TINYINT UNSIGNED = %d, want 255", tinyUnsigned)
	}

	var small int16
	queryPreparedValue(t, db, ctx, "SELECT small_value FROM values_for_binary_results", &small)
	if small != 32000 {
		t.Fatalf("prepared SMALLINT = %d, want 32000", small)
	}

	var smallUnsigned uint16
	queryPreparedValue(t, db, ctx, "SELECT small_unsigned_value FROM values_for_binary_results", &smallUnsigned)
	if smallUnsigned != 65535 {
		t.Fatalf("prepared SMALLINT UNSIGNED = %d, want 65535", smallUnsigned)
	}

	var medium int32
	queryPreparedValue(t, db, ctx, "SELECT medium_value FROM values_for_binary_results", &medium)
	if medium != 8388607 {
		t.Fatalf("prepared MEDIUMINT = %d, want 8388607", medium)
	}

	var mediumUnsigned uint32
	queryPreparedValue(t, db, ctx, "SELECT medium_unsigned_value FROM values_for_binary_results", &mediumUnsigned)
	if mediumUnsigned != 16777215 {
		t.Fatalf("prepared MEDIUMINT UNSIGNED = %d, want 16777215", mediumUnsigned)
	}

	var floatValue float32
	queryPreparedValue(t, db, ctx, "SELECT float_value FROM values_for_binary_results", &floatValue)
	if math.Float32bits(floatValue) != math.Float32bits(1.25) {
		t.Fatalf("prepared FLOAT = %v, want 1.25", floatValue)
	}

	var bitValue []byte
	queryPreparedValue(t, db, ctx, "SELECT bit_value FROM values_for_binary_results", &bitValue)
	if !bytes.Equal(bitValue, []byte{10}) {
		t.Fatalf("prepared BIT(8) = %x, want 0a", bitValue)
	}
}

func queryPreparedValue(t *testing.T, db *sql.DB, ctx context.Context, query string, destination any) {
	t.Helper()
	statement, err := db.PrepareContext(ctx, query)
	if err != nil {
		t.Fatalf("prepare %s: %v", query, err)
	}
	defer statement.Close()
	if err := statement.QueryRowContext(ctx).Scan(destination); err != nil {
		t.Fatalf("execute %s: %v", query, err)
	}
}
