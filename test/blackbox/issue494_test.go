package blackbox_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue494PreparedStatementLimitUsesSyntaxSQLState(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory, "--max-prepared-stmt-count", "2")
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
	db.SetMaxOpenConns(2)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstSession, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer firstSession.Close()
	for _, query := range []string{"SELECT 1", "SELECT 2"} {
		statement, err := firstSession.PrepareContext(ctx, query)
		if err != nil {
			t.Fatalf("prepare %q on first session: %v", query, err)
		}
		defer statement.Close()
	}

	secondSession, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer secondSession.Close()
	_, err = secondSession.PrepareContext(ctx, "SELECT 3")
	if err == nil {
		t.Fatal("third prepared statement succeeded above the configured limit")
	}
	var mysqlError *mysql.MySQLError
	if !errors.As(err, &mysqlError) {
		t.Fatalf("third prepare error = %T %v, want MySQL error 1461 (42000)", err, err)
	}
	if mysqlError.Number != 1461 || string(mysqlError.SQLState[:]) != "42000" {
		t.Fatalf("third prepare error = %v (code %d, SQLSTATE %s), want error 1461 (42000)", err, mysqlError.Number, mysqlError.SQLState)
	}
}
