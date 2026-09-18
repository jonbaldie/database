package blackbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

// TestIssue400CatalogShowClauses proves the documented FROM/IN, LIKE, and
// WHERE forms of the catalog SHOW surface work over the MySQL wire.
func TestIssue400CatalogShowClauses(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openIssue364Database(address)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for _, statement := range []string{"CREATE DATABASE odku", "USE odku", "CREATE TABLE t (id INT PRIMARY KEY, q INT)"} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	for _, statement := range []string{
		"SHOW COLUMNS FROM t", "SHOW COLUMNS FROM odku.t", "SHOW COLUMNS FROM `t`", "SHOW COLUMNS IN t",
		"SHOW COLUMNS FROM t LIKE 'q%'", "SHOW FULL COLUMNS FROM t", "DESCRIBE t", "DESC t", "DESCRIBE odku.t",
		"SHOW TABLES FROM odku", "SHOW TABLES IN odku", "SHOW FULL TABLES", "SHOW FULL TABLES FROM odku",
		"SHOW TABLES WHERE Tables_in_odku = 't'", "SHOW INDEX FROM t FROM odku",
		"SHOW CHARACTER SET", "SHOW COLLATION",
	} {
		rows, err := conn.QueryContext(ctx, statement)
		if err != nil {
			t.Errorf("%s: %v", statement, err)
			continue
		}
		count := 0
		for rows.Next() {
			count++
		}
		if err := rows.Err(); err != nil || count == 0 {
			t.Errorf("%s: rows = %d, err = %v", statement, count, err)
		}
		_ = rows.Close()
	}
}
