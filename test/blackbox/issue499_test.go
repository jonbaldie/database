package blackbox_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue499OversizedPacketsReportPacketTooLarge(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory, "--max-allowed-packet", "1024")
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	config := mysql.NewConfig()
	config.User = "admin"
	config.Passwd = "lifecycle-secret"
	config.Net = "tcp"
	config.Addr = address
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	inbound, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var length int
	err = inbound.QueryRowContext(ctx, "SELECT LENGTH('"+strings.Repeat("x", 1100)+"') AS l").Scan(&length)
	assertPacketTooLarge(t, "oversized inbound COM_QUERY", err)
	_ = inbound.Close()

	session, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, statement := range []string{
		"CREATE DATABASE k",
		"CREATE TABLE k.w (id INT PRIMARY KEY, v VARCHAR(700))",
		"INSERT INTO k.w VALUES (1, '" + strings.Repeat("y", 600) + "')",
	} {
		if _, err := session.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement[:min(len(statement), 40)], err)
		}
	}
	var doubled string
	err = session.QueryRowContext(ctx, "SELECT CONCAT(v,v) FROM k.w").Scan(&doubled)
	assertPacketTooLarge(t, "oversized outbound row", err)

	var one int
	if err := session.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1 after oversized row = %d, %v; want 1 on the same session", one, err)
	}
}

func assertPacketTooLarge(t *testing.T, label string, err error) {
	t.Helper()
	var mysqlError *mysql.MySQLError
	if !errors.As(err, &mysqlError) {
		t.Fatalf("%s error = %T %v, want MySQL error 1153 (08S01)", label, err, err)
	}
	if mysqlError.Number != 1153 || string(mysqlError.SQLState[:]) != "08S01" {
		t.Fatalf("%s error = %v (code %d, SQLSTATE %s), want error 1153 (08S01)", label, err, mysqlError.Number, mysqlError.SQLState)
	}
}
