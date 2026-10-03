package blackbox_test

import (
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue490RejectsIncompatibleForeignKeyColumnTypesThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE issue490",
		"USE issue490",
		"CREATE TABLE parent (id INT PRIMARY KEY, code VARCHAR(20) UNIQUE)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}

	for _, tc := range []struct {
		name  string
		table string
		query string
	}{
		{"varchar", "child_varchar", "CREATE TABLE child_varchar (a VARCHAR(5), FOREIGN KEY (a) REFERENCES parent (id))"},
		{"date", "child_date", "CREATE TABLE child_date (a DATE, FOREIGN KEY (a) REFERENCES parent (id))"},
		{"integer width", "child_bigint", "CREATE TABLE child_bigint (a BIGINT, FOREIGN KEY (a) REFERENCES parent (id))"},
		{"integer sign", "child_unsigned", "CREATE TABLE child_unsigned (a INT UNSIGNED, FOREIGN KEY (a) REFERENCES parent (id))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if result := client.query(tc.query); result.errCode != 3780 {
				t.Fatalf("CREATE TABLE accepted or returned the wrong error for incompatible foreign-key types: %#v", result)
			}
			if result := client.query("SHOW TABLES LIKE '" + tc.table + "'"); result.err != "" || len(result.rows) != 0 {
				t.Fatalf("failed CREATE TABLE left %s in the catalog: %#v", tc.table, result)
			}
		})
	}
	for _, query := range []string{
		"CREATE TABLE compatible_string (a VARCHAR(5), FOREIGN KEY (a) REFERENCES parent (code))",
		"CREATE TABLE compatible_integer_alias (a INTEGER, FOREIGN KEY (a) REFERENCES parent (id))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("compatible foreign-key types rejected by %q: %#v", query, result)
		}
	}
}

func TestIssue490AlterTableRejectsIncompatibleForeignKeyColumnTypesThroughMySQL(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE issue490",
		"USE issue490",
		"CREATE TABLE parent (id INT PRIMARY KEY)",
		"CREATE TABLE child (a VARCHAR(5))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup query %q: %#v", query, result)
		}
	}
	if result := client.query("ALTER TABLE child ADD CONSTRAINT fk_child FOREIGN KEY (a) REFERENCES parent (id)"); result.errCode != 3780 {
		t.Fatalf("ALTER TABLE accepted or returned the wrong error for incompatible foreign-key types: %#v", result)
	}
	if result := client.query("SHOW CREATE TABLE child"); result.err != "" || len(result.rows) != 1 || strings.Contains(result.rows[0][1], "FOREIGN KEY") {
		t.Fatalf("failed ALTER TABLE changed the child definition: %#v", result)
	}
}
