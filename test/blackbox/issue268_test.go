package blackbox_test

import (
	"reflect"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue268LikeEmptyEscapeDisablesEscape(t *testing.T) {
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
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE t (id INT PRIMARY KEY, val VARCHAR(20))",
		"INSERT INTO t VALUES (1, 'a%b'), (2, 'axb'), (3, 'aXb'), (4, 'a_b')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	// ESCAPE '' disables escape processing: % and _ stay wildcards and a
	// backslash in the pattern is literal, matching MySQL 8.4.
	result := client.query("SELECT id FROM t WHERE val LIKE 'a%b' ESCAPE '' ORDER BY id")
	if result.err != "" {
		t.Fatalf("select with empty escape: %#v", result)
	}
	want := [][]string{{"1"}, {"2"}, {"3"}, {"4"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows with empty escape = %#v, want %#v", result.rows, want)
	}

	result = client.query("SELECT id FROM t WHERE val LIKE 'a_b' ESCAPE '' ORDER BY id")
	if result.err != "" {
		t.Fatalf("select with empty escape and underscore: %#v", result)
	}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows with empty escape and underscore = %#v, want %#v", result.rows, want)
	}

	// A backslash in the pattern is no longer an escape character.
	result = client.query("SELECT id FROM t WHERE val LIKE 'a\\_b' ESCAPE '' ORDER BY id")
	if result.err != "" {
		t.Fatalf("select with literal backslash: %#v", result)
	}
	if len(result.rows) != 0 {
		t.Fatalf("rows with literal backslash = %#v, want none", result.rows)
	}

	// An escape sequence longer than one character is still rejected.
	result = client.query("SELECT id FROM t WHERE val LIKE 'a%b' ESCAPE 'ab'")
	if result.errCode != 1210 {
		t.Fatalf("multi-character escape error code = %d, want 1210: %#v", result.errCode, result)
	}
}
