package blackbox_test

import (
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue400ShowCatalogFromInWhereAndCharset(t *testing.T) {
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
		"CREATE DATABASE odku",
		"USE odku",
		"CREATE TABLE t (id INT PRIMARY KEY, q VARCHAR(8))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	from := client.query("SHOW TABLES FROM odku")
	if from.err != "" || !equalStrings(from.columns, []string{"Tables_in_odku"}) || !equalStringRows(from.rows, [][]string{{"t"}}) {
		t.Fatalf("SHOW TABLES FROM odku: %#v", from)
	}
	in := client.query("SHOW TABLES IN odku")
	if in.err != "" || !equalStringRows(in.rows, [][]string{{"t"}}) {
		t.Fatalf("SHOW TABLES IN odku: %#v", in)
	}
	full := client.query("SHOW FULL TABLES FROM odku")
	if full.err != "" || !equalStrings(full.columns, []string{"Tables_in_odku", "Table_type"}) || !equalStringRows(full.rows, [][]string{{"t", "BASE TABLE"}}) {
		t.Fatalf("SHOW FULL TABLES FROM odku: %#v", full)
	}
	where := client.query("SHOW TABLES WHERE Tables_in_odku = 't'")
	if where.err != "" || !equalStringRows(where.rows, [][]string{{"t"}}) {
		t.Fatalf("SHOW TABLES WHERE: %#v", where)
	}

	charset := client.query("SHOW CHARACTER SET")
	if charset.err != "" || !equalStrings(charset.columns, []string{"Charset", "Description", "Default collation", "Maxlen"}) || len(charset.rows) != 1 || charset.rows[0][0] != "utf8mb4" {
		t.Fatalf("SHOW CHARACTER SET: %#v", charset)
	}
	collation := client.query("SHOW COLLATION")
	if collation.err != "" || len(collation.rows) != 2 || collation.rows[0][0] != "utf8mb4_0900_ai_ci" || collation.rows[1][0] != "utf8mb4_bin" {
		t.Fatalf("SHOW COLLATION: %#v", collation)
	}

	indexes := client.query("SHOW INDEX FROM t FROM odku")
	if indexes.err != "" || len(indexes.rows) != 1 || indexes.rows[0][0] != "t" || indexes.rows[0][2] != "PRIMARY" {
		t.Fatalf("SHOW INDEX FROM t FROM odku: %#v", indexes)
	}
	columns := client.query("SHOW COLUMNS FROM t FROM odku")
	if columns.err != "" || len(columns.rows) != 2 || columns.rows[0][0] != "id" {
		t.Fatalf("SHOW COLUMNS FROM t FROM odku: %#v", columns)
	}
}
