package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue527UniqueConstraintVisibilityCanBeAltered(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, tc := range []struct {
		name     string
		database string
		create   string
		add      string
		index    string
	}{
		{
			name:     "named table constraint",
			database: "app_named",
			create: `CREATE TABLE accounts (
				id INT PRIMARY KEY,
				email VARCHAR(64),
				CONSTRAINT uq_email UNIQUE (email)
			)`,
			index: "uq_email",
		},
		{
			name:     "inline column constraint",
			database: "app_inline",
			create:   "CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64) UNIQUE)",
			index:    "accounts_email_unique",
		},
		{
			name:     "constraint added by ALTER TABLE",
			database: "app_added",
			create:   "CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64))",
			add:      "ALTER TABLE accounts ADD CONSTRAINT uq_email UNIQUE (email)",
			index:    "uq_email",
		},
		{
			name:     "unique index control",
			database: "app_index",
			create:   "CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(64), UNIQUE INDEX uq_email (email))",
			index:    "uq_email",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, query := range []string{
				"CREATE DATABASE " + tc.database,
				"USE " + tc.database,
				tc.create,
				tc.add,
			} {
				if query == "" {
					continue
				}
				if result := client.query(query); result.err != "" {
					t.Fatalf("setup query %q: %#v", query, result)
				}
			}

			if result := client.query("SHOW INDEX FROM accounts"); result.err != "" || !showIndexContains(result, tc.index, "email", "A", "YES") {
				t.Fatalf("unique constraint index metadata: %#v", result)
			}
			if result := client.query("ALTER TABLE accounts ALTER INDEX " + tc.index + " INVISIBLE"); result.err != "" {
				t.Fatalf("make unique constraint index invisible: %#v", result)
			}
			if result := client.query("SHOW INDEX FROM accounts"); result.err != "" || !showIndexContains(result, tc.index, "email", "A", "NO") {
				t.Fatalf("invisible unique constraint index metadata: %#v", result)
			}
			if result := client.query("ALTER TABLE accounts ALTER INDEX " + tc.index + " VISIBLE"); result.err != "" {
				t.Fatalf("make unique constraint index visible: %#v", result)
			}
			if result := client.query("SHOW INDEX FROM accounts"); result.err != "" || !showIndexContains(result, tc.index, "email", "A", "YES") {
				t.Fatalf("visible unique constraint index metadata: %#v", result)
			}
		})
	}
}
