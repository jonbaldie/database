package blackbox_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func executableProductVersion(t *testing.T) string {
	t.Helper()
	runner := blackbox.Runner{Executable: executable}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := runner.Run(ctx, "version", "--format=json")
	var version struct {
		ProductVersion string `json:"product_version"`
	}
	if result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &version) != nil || version.ProductVersion == "" {
		t.Fatalf("version report: %#v", result)
	}
	return version.ProductVersion
}

func TestIssue546SQLAndExplanationReportExecutableVersion(t *testing.T) {
	productVersion := executableProductVersion(t)
	t.Logf("executable product_version = %s", productVersion)
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, query := range []string{"SELECT VERSION()", "SELECT @@version", "SHOW SESSION VARIABLES LIKE 'version'"} {
		t.Run(query, func(t *testing.T) {
			var value string
			var err error
			if query == "SHOW SESSION VARIABLES LIKE 'version'" {
				var name string
				err = db.QueryRowContext(ctx, query).Scan(&name, &value)
			} else {
				err = db.QueryRowContext(ctx, query).Scan(&value)
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := "8.4.11-database-" + productVersion; value != want {
				t.Fatalf("SQL version = %q, want %q", value, want)
			}
		})
	}
	for _, query := range []string{"EXPLAIN FORMAT=JSON SELECT 1", "EXPLAIN ANALYZE FORMAT=JSON SELECT 1"} {
		t.Run(query, func(t *testing.T) {
			var document string
			if err := db.QueryRowContext(ctx, query).Scan(&document); err != nil {
				t.Fatal(err)
			}
			var explanation struct {
				ServerVersion string `json:"server_version"`
			}
			if err := json.Unmarshal([]byte(document), &explanation); err != nil {
				t.Fatal(err)
			}
			if explanation.ServerVersion != productVersion {
				t.Fatalf("EXPLAIN server_version = %q, want %q", explanation.ServerVersion, productVersion)
			}
		})
	}
}
