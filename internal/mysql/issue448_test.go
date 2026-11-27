package mysql

import (
	"database/sql"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/credential"
)

func openIssue448TestDB(t *testing.T) *sql.DB {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Rows().Close() })

	server, err := NewWithConfig("127.0.0.1:0", Config{
		Catalog:      store,
		Username:     "admin",
		PasswordHash: credential.PasswordHash("issue448-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { _ = server.CloseGracefully() })

	cfg := driver.NewConfig()
	cfg.User, cfg.Passwd = "admin", "issue448-secret"
	cfg.Net, cfg.Addr = "tcp", server.Listener.Addr().String()
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

func assertLastInsertIDAndAffected(t *testing.T, res sql.Result, wantID, wantAffected int64, label string) {
	t.Helper()
	gotID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("%s LastInsertId: %v", label, err)
	}
	if gotID != wantID {
		t.Fatalf("%s LastInsertId = %d, want %d", label, gotID, wantID)
	}
	gotAffected, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("%s RowsAffected: %v", label, err)
	}
	if gotAffected != wantAffected {
		t.Fatalf("%s RowsAffected = %d, want %d", label, gotAffected, wantAffected)
	}
}

func TestIssue448AutoIncrementLastInsertID(t *testing.T) {
	db := openIssue448TestDB(t)
	for _, query := range []string{
		"CREATE DATABASE trial",
		"USE trial",
		"CREATE TABLE ids (id BIGINT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(20) NOT NULL)",
	} {
		res, err := db.Exec(query)
		if err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
		assertLastInsertIDAndAffected(t, res, 0, 0, "setup "+query)
	}

	// Single-row text insert -> 1.
	res, err := db.Exec("INSERT INTO ids (label) VALUES ('one')")
	if err != nil {
		t.Fatalf("single insert: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 1, 1, "single text insert")

	// Multi-row text insert -> 2 (first generated id).
	res, err = db.Exec("INSERT INTO ids (label) VALUES ('two'), ('three')")
	if err != nil {
		t.Fatalf("multi insert: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 2, 2, "multi text insert")

	// Explicit non-NULL id -> 0 (no value generated).
	res, err = db.Exec("INSERT INTO ids (id, label) VALUES (10, 'explicit')")
	if err != nil {
		t.Fatalf("explicit insert: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 0, 1, "explicit id insert")

	// Non-insert DML (UPDATE / DELETE) -> 0.
	res, err = db.Exec("UPDATE ids SET label = 'updated' WHERE id = 10")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 0, 1, "update")

	res, err = db.Exec("DELETE FROM ids WHERE id = 10")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 0, 1, "delete")

	// Next generated id after explicit 10 -> 11.
	res, err = db.Exec("INSERT INTO ids (label) VALUES ('after-explicit')")
	if err != nil {
		t.Fatalf("after explicit insert: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 11, 1, "after explicit")
}

func TestIssue448PreparedStatementLastInsertID(t *testing.T) {
	db := openIssue448TestDB(t)
	for _, query := range []string{
		"CREATE DATABASE trial",
		"USE trial",
		"CREATE TABLE ids (id BIGINT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(20) NOT NULL)",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	stmtOmit, err := db.Prepare("INSERT INTO ids (label) VALUES (?)")
	if err != nil {
		t.Fatalf("prepare omit: %v", err)
	}
	defer stmtOmit.Close()

	res, err := stmtOmit.Exec("prep-one")
	if err != nil {
		t.Fatalf("exec prep-one: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 1, 1, "prepared omit 1")

	res, err = stmtOmit.Exec("prep-two")
	if err != nil {
		t.Fatalf("exec prep-two: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 2, 1, "prepared omit 2")

	// Prepared statement with explicit NULL for the AUTO_INCREMENT column.
	stmtNull, err := db.Prepare("INSERT INTO ids (id, label) VALUES (?, ?)")
	if err != nil {
		t.Fatalf("prepare null: %v", err)
	}
	defer stmtNull.Close()

	res, err = stmtNull.Exec(nil, "prep-null")
	if err != nil {
		t.Fatalf("exec prep-null: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 3, 1, "prepared explicit NULL")

	// Prepared statement with explicit non-NULL id -> 0.
	res, err = stmtNull.Exec(20, "prep-explicit")
	if err != nil {
		t.Fatalf("exec prep-explicit: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 0, 1, "prepared explicit non-NULL")
}

func TestIssue448InsertSelectAndPartialFailure(t *testing.T) {
	db := openIssue448TestDB(t)
	for _, query := range []string{
		"CREATE DATABASE trial",
		"USE trial",
		"CREATE TABLE src (k BIGINT, label VARCHAR(20) NOT NULL)",
		"INSERT INTO src (k, label) VALUES (100, 'a'), (101, 'b')",
		"CREATE TABLE ids (id BIGINT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(20) NOT NULL)",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	// INSERT ... SELECT generating AUTO_INCREMENT values -> first generated id (1).
	res, err := db.Exec("INSERT INTO ids (label) SELECT label FROM src ORDER BY k")
	if err != nil {
		t.Fatalf("insert select generated: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 1, 2, "insert select generated")

	// INSERT ... SELECT supplying explicit non-NULL ids -> 0.
	res, err = db.Exec("INSERT INTO ids (id, label) SELECT k, label FROM src ORDER BY k")
	if err != nil {
		t.Fatalf("insert select explicit: %v", err)
	}
	assertLastInsertIDAndAffected(t, res, 0, 2, "insert select explicit")

	// Multi-row insert that fails partway (row 2 duplicates PK 100) -> error, no partial commit.
	if _, err := db.Exec("INSERT INTO ids (id, label) VALUES (NULL, 'ok'), (100, 'dup')"); err == nil {
		t.Fatal("expected error for partial multi-row insert failure, got nil")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM ids").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 4 {
		t.Fatalf("row count after failed multi-row insert = %d, want 4", count)
	}
}
