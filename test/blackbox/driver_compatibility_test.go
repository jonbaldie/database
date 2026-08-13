package blackbox_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

// TestGoDriverCompatibilityProfile is the always-on reference-driver profile.
// The other language clients use the same profile through TestExternalDriverCompatibility
// when DATABASE_COMPATIBILITY_DRIVERS=1 is set. Keeping the scenarios here in terms of
// the public socket makes this a real end-to-end test rather than an internal unit test.
func TestGoDriverCompatibilityProfile(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dsn := "admin:lifecycle-secret@tcp(" + address + ")/?parseTime=true"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS compatibility"); err != nil {
		t.Fatalf("create compatibility schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "USE compatibility"); err != nil {
		t.Fatalf("select compatibility schema: %v", err)
	}

	assertSingleValue(t, db, ctx, "SELECT VERSION()", "8.4.11-database-0.2.0-dev")
	assertSingleValue(t, db, ctx, "SELECT @@time_zone", "+00:00")
	if _, err := db.ExecContext(ctx, "SET time_zone = '+05:30'"); err != nil {
		t.Fatalf("set session variable: %v", err)
	}
	assertSingleValue(t, db, ctx, "SELECT @@time_zone", "+05:30")

	statement, err := db.PrepareContext(ctx, "SELECT ? AS name, NULL AS empty, 7 AS number")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer statement.Close()
	var name, number sql.NullString
	var nullValue any
	if err := statement.QueryRowContext(ctx, "Ada").Scan(&name, &nullValue, &number); err != nil {
		t.Fatalf("execute prepared query: %v", err)
	}
	if !name.Valid || name.String != "Ada" || nullValue != nil || !number.Valid || number.String != "7" {
		t.Fatalf("prepared values = (%#v, %#v, %#v)", name, nullValue, number)
	}

	rows, err := db.QueryContext(ctx, "SELECT 'Ada' AS name, 7 AS number")
	if err != nil {
		t.Fatalf("text query: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(columns, ",") != "name,number" {
		t.Fatalf("columns = %v", columns)
	}
	if !rows.Next() {
		t.Fatal("text query returned no row")
	}
	var textName string
	var textNumber int
	if err := rows.Scan(&textName, &textNumber); err != nil {
		t.Fatal(err)
	}
	if textName != "Ada" || textNumber != 7 {
		t.Fatalf("text values = %q, %d", textName, textNumber)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, "SELECT * FROM no_such_table"); err == nil {
		t.Fatal("unknown table unexpectedly succeeded")
	} else {
		var mysqlError *mysql.MySQLError
		if !errors.As(err, &mysqlError) || mysqlError.Number != 1146 || string(mysqlError.SQLState[:]) != "42S02" {
			t.Fatalf("unknown table error = %v", err)
		}
	}
}

func assertSingleValue(t *testing.T, db *sql.DB, ctx context.Context, query, want string) {
	t.Helper()
	var value string
	if err := db.QueryRowContext(ctx, query).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if value != want {
		t.Fatalf("%s = %q, want %q", query, value, want)
	}
}

// TestExternalDriverCompatibilityProfile runs unmodified PHP, Node, Python,
// and Java clients plus the mysql CLI. It is opt-in because the project does
// not vendor language package managers. With the gate enabled, missing tools
// are failures, so a green run is evidence for every listed client.
func TestExternalDriverCompatibilityProfile(t *testing.T) {
	if os.Getenv("DATABASE_COMPATIBILITY_DRIVERS") != "1" {
		t.Skip("set DATABASE_COMPATIBILITY_DRIVERS=1 to run external driver conformance")
	}
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	certificate, key := testTLSCertificate(t)
	address := freeAddress(t)
	process, err := runner.Start(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", address, "--tls-certificate-file", certificate, "--tls-private-key-file", key, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	nextReadyEvent(t, process)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	host, port, err := splitAddress(address)
	if err != nil {
		t.Fatal(err)
	}
	root := repositoryRoot()
	env := append(os.Environ(),
		"DATABASE_COMPAT_HOST="+host,
		"DATABASE_COMPAT_PORT="+port,
		"DATABASE_COMPAT_USER=admin",
		"DATABASE_COMPAT_PASSWORD=lifecycle-secret",
		"DATABASE_COMPAT_TLS=1",
	)

	run := func(name, command string, args ...string) {
		t.Helper()
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cmd := exec.CommandContext(context.Background(), command, args...)
		cmd.Dir = root
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, output)
		}
	}

	run("PHP PDO/mysqli", "php", filepath.Join(root, "scripts", "compatibility", "php.php"))
	run("Node mysql2", "node", filepath.Join(root, "scripts", "compatibility", "node.js"))
	run("Python Connector/Python", "python3", filepath.Join(root, "scripts", "compatibility", "python.py"))

	jar := os.Getenv("MYSQL_CONNECTOR_JAR")
	if jar == "" {
		t.Fatal("MYSQL_CONNECTOR_JAR must point to the pinned Connector/J jar")
	}
	javaDir := t.TempDir()
	if output, err := exec.Command("javac", "-cp", jar, "-d", javaDir, filepath.Join(root, "scripts", "compatibility", "Compatibility.java")).CombinedOutput(); err != nil {
		t.Fatalf("compile Connector/J probe: %v\n%s", err, output)
	}
	runJava := exec.Command("java", "-cp", javaDir+string(os.PathListSeparator)+jar, "Compatibility")
	runJava.Dir = root
	runJava.Env = env
	if output, err := runJava.CombinedOutput(); err != nil {
		t.Fatalf("Java Connector/J: %v\n%s", err, output)
	}

	run("mysql CLI", "mysql", "--protocol=tcp", "--ssl-mode=REQUIRED", "--host="+host, "--port="+port, "--user=admin", "--password=lifecycle-secret", "--batch", "--skip-column-names", "-e", "SELECT VERSION()")
}

func splitAddress(address string) (string, string, error) {
	parts := strings.Split(address, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("unexpected server address %q", address)
	}
	return parts[0], parts[1], nil
}

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}
