package blackbox_test

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

// TestIssue492IdleTimeoutsAreReportedAndEnforced proves that the configured
// idle timeouts are visible to sessions, that an idle transaction is rolled
// back and its session closed, and that an idle session without a
// transaction is closed only after the longer idle-session timeout.
func TestIssue492IdleTimeoutsAreReportedAndEnforced(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory,
		"--idle-session-timeout-ms", "3000", "--idle-in-transaction-timeout-ms", "1000")
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE DATABASE issue492",
		"CREATE TABLE issue492.t (id INT PRIMARY KEY, v INT)",
		"INSERT INTO issue492.t VALUES (1, 10)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("execute %q: %v", statement, err)
		}
	}

	sessionA, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionA.Close()
	var idleSession, idleTransaction string
	if err := sessionA.QueryRowContext(ctx, "SELECT @@idle_session_timeout_ms, @@idle_in_transaction_timeout_ms").Scan(&idleSession, &idleTransaction); err != nil {
		t.Fatal(err)
	}
	if idleSession != "3000" || idleTransaction != "1000" {
		t.Fatalf("idle timeouts = %s, %s; want 3000, 1000", idleSession, idleTransaction)
	}

	for _, statement := range []string{"BEGIN", "UPDATE issue492.t SET v = 99 WHERE id = 1"} {
		if _, err := sessionA.ExecContext(ctx, statement); err != nil {
			t.Fatalf("session A %q: %v", statement, err)
		}
	}
	sessionC, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionC.Close()
	if _, err := sessionC.ExecContext(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	sessionB, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionB.Close()
	if _, err := sessionB.ExecContext(ctx, "SET lock_wait_timeout_ms = 500"); err != nil {
		t.Fatal(err)
	}
	var value int
	if err := sessionB.QueryRowContext(ctx, "SELECT v FROM issue492.t WHERE id = 1 FOR UPDATE").Scan(&value); err != nil {
		t.Fatalf("session B locking read after session A idled in transaction: %v", err)
	}
	if value != 10 {
		t.Fatalf("session B read v = %d, want rolled-back value 10", value)
	}
	if _, err := sessionA.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("session A remained usable after its transaction idled past the timeout")
	}
	if _, err := sessionC.ExecContext(ctx, "SELECT 1"); err != nil {
		t.Fatalf("session C without a transaction closed before the idle-session timeout: %v", err)
	}

	time.Sleep(3500 * time.Millisecond)
	if _, err := sessionB.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("session B remained usable after it idled past the idle-session timeout")
	}
}

// TestIssue492IdleExpiryEmitsStructuredDiagnostic proves that the server sends
// an inactivity error packet before it closes an idle session.
func TestIssue492IdleExpiryEmitsStructuredDiagnostic(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory,
		"--idle-session-timeout-ms", "600", "--idle-in-transaction-timeout-ms", "300")
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	for _, opening := range []string{"", "BEGIN"} {
		client := newWireClient(t, address, "admin", "lifecycle-secret")
		if opening != "" {
			client.query(opening)
		}
		_ = client.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		payload := readWirePacket(t, client.conn)
		assertWireError(t, payload, 4031, "HY000")
		_, err := client.conn.Read(make([]byte, 1))
		var netErr net.Error
		if err == nil || (errors.As(err, &netErr) && netErr.Timeout()) {
			t.Fatalf("session after %q not closed after diagnostic: %v", opening, err)
		}
		_ = client.conn.Close()
	}
}
