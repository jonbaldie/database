package blackbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

// Issue 461: namespace comparisons in DROP DATABASE and RENAME TABLE must use
// canonical caseless matching, so `straße` and `STRASSE` (full case folding)
// and NFC and NFD spellings name one namespace.
func TestIssue461CanonicallyEquivalentNamespaceSpellings(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	exec := func(statement string) {
		t.Helper()
		if _, err := session.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	exec("CREATE DATABASE `straße`")
	exec("CREATE TABLE `straße`.t (id INT PRIMARY KEY)")
	exec("RENAME TABLE `straße`.t TO `STRASSE`.u")
	var name string
	if err := session.QueryRowContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'straße'").Scan(&name); err != nil || name != "u" {
		t.Fatalf("renamed table = %q, %v; want u", name, err)
	}

	exec("USE `straße`")
	exec("DROP DATABASE `STRASSE`")
	var selected sql.NullString
	if err := session.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if selected.Valid {
		t.Fatalf("SELECT DATABASE() after DROP DATABASE `STRASSE` = %q; want NULL", selected.String)
	}

	// "é" as NFC (U+00E9) and as NFD (e + U+0301) names one namespace.
	exec("CREATE DATABASE `café`")
	exec("CREATE TABLE `café`.t (id INT PRIMARY KEY)")
	exec("RENAME TABLE `café`.t TO `CAFÉ`.u")
	exec("USE `café`")
	exec("DROP DATABASE `café`")
	if err := session.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if selected.Valid {
		t.Fatalf("SELECT DATABASE() after NFD DROP DATABASE = %q; want NULL", selected.String)
	}
}
