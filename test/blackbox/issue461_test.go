package blackbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue461CanonicalNamespaceIdentityOnWire(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		"CREATE DATABASE `straße`", "CREATE TABLE `straße`.t (id INT)",
		"INSERT INTO `straße`.t VALUES (7)", "RENAME TABLE `straße`.t TO `STRASSE`.u",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	var id int
	if err := db.QueryRowContext(ctx, "SELECT id FROM `straße`.u").Scan(&id); err != nil || id != 7 {
		t.Fatalf("renamed table id = %d, err = %v", id, err)
	}
	for _, query := range []string{"USE `straße`", "DROP DATABASE `STRASSE`"} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	var selected sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&selected); err != nil || selected.Valid {
		t.Fatalf("selected database = %#v, err = %v; want NULL", selected, err)
	}
}
