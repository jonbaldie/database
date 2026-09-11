package mysql

import (
	"strings"
	"testing"
)

func TestIssue228AutoIncrementBasic(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY AUTO_INCREMENT, val INT)",
		"INSERT INTO t (val) VALUES (10), (20)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}

	result, err := executeStatement(executor, "SELECT id, val FROM t ORDER BY id")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"1", "10"}, {"2", "20"}}) {
		t.Fatalf("rows = %#v", result.rows)
	}
}

func TestIssue228AutoIncrementRatchetsAndTruncates(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY AUTO_INCREMENT, val INT)",
		"INSERT INTO t (val) VALUES (10), (20)",
		"INSERT INTO t (id, val) VALUES (7, 70), (NULL, 80), (DEFAULT, 90)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, val FROM t ORDER BY id")
	if err != nil || !equalRows(result.rows, [][]string{{"1", "10"}, {"2", "20"}, {"7", "70"}, {"8", "80"}, {"9", "90"}}) {
		t.Fatalf("ratcheted rows = %#v, err = %v", result.rows, err)
	}
	if _, err := executeStatement(executor, "DELETE FROM t WHERE id = 9"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO t (val) VALUES (100)"); err != nil {
		t.Fatalf("insert after delete: %v", err)
	}
	result, err = executeStatement(executor, "SELECT id, val FROM t ORDER BY id")
	if err != nil || !equalRows(result.rows, [][]string{{"1", "10"}, {"2", "20"}, {"7", "70"}, {"8", "80"}, {"10", "100"}}) {
		t.Fatalf("delete did not reuse an id: rows = %#v, err = %v", result.rows, err)
	}
	if _, err := executeStatement(executor, "TRUNCATE TABLE t"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	for _, query := range []string{
		"INSERT INTO t (val) VALUES (110)",
		"INSERT INTO t (id, val) VALUES (5, 150)",
		"INSERT INTO t (id, val) VALUES (3, 130)",
		"INSERT INTO t (val) VALUES (160)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute after truncate %q: %v", query, err)
		}
	}
	result, err = executeStatement(executor, "SELECT id, val FROM t ORDER BY id")
	if err != nil || !equalRows(result.rows, [][]string{{"1", "110"}, {"3", "130"}, {"5", "150"}, {"6", "160"}}) {
		t.Fatalf("truncated rows = %#v, err = %v", result.rows, err)
	}
}

func TestIssue228AutoIncrementMetadata(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY AUTO_INCREMENT, val INT)",
		"INSERT INTO t (val) VALUES (10), (20)",
		"INSERT INTO t (id, val) VALUES (7, 70)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT AUTO_INCREMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 't'")
	if err != nil || !equalRows(result.rows, [][]string{{"8"}}) {
		t.Fatalf("TABLES.AUTO_INCREMENT = %#v, err = %v", result.rows, err)
	}
	result, err = executeStatement(executor, "SELECT EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = 'app' AND TABLE_NAME = 't' AND COLUMN_NAME = 'id'")
	if err != nil || !equalRows(result.rows, [][]string{{"auto_increment"}}) {
		t.Fatalf("COLUMNS.EXTRA = %#v, err = %v", result.rows, err)
	}
	result, err = executeStatement(executor, "SHOW CREATE TABLE t")
	if err != nil {
		t.Fatalf("show create: %v", err)
	}
	definition := strings.Join(result.rows[0], "\n")
	for _, expected := range []string{"AUTO_INCREMENT", "AUTO_INCREMENT=8"} {
		if !strings.Contains(definition, expected) {
			t.Fatalf("SHOW CREATE TABLE = %q, missing %q", definition, expected)
		}
	}

	replayExecutor := ddlExecutorForTest(t)
	if _, err := executeStatement(replayExecutor, result.rows[0][1]); err != nil {
		t.Fatalf("replay SHOW CREATE TABLE: %v", err)
	}
	if _, err := executeStatement(replayExecutor, "INSERT INTO t (val) VALUES (80)"); err != nil {
		t.Fatalf("insert into replayed table: %v", err)
	}
	result, err = executeStatement(replayExecutor, "SELECT id, val FROM t")
	if err != nil || !equalRows(result.rows, [][]string{{"8", "80"}}) {
		t.Fatalf("replayed table rows = %#v, err = %v", result.rows, err)
	}
}

func TestIssue228AutoIncrementRequiresIndexedIntegerColumn(t *testing.T) {
	cases := []string{
		"CREATE TABLE no_key (id INT AUTO_INCREMENT, val INT)",
		"CREATE TABLE wrong_type (id VARCHAR(10) PRIMARY KEY AUTO_INCREMENT)",
		"CREATE TABLE multiple (a INT PRIMARY KEY AUTO_INCREMENT, b INT UNIQUE AUTO_INCREMENT)",
	}
	for _, query := range cases {
		executor := ddlExecutorForTest(t)
		if _, err := executeStatement(executor, query); err == nil {
			t.Errorf("execute %q succeeded", query)
		}
	}

	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE secondary (id INT AUTO_INCREMENT, val INT, INDEX (id))"); err != nil {
		t.Fatalf("indexed auto column: %v", err)
	}
	if _, err := executeStatement(executor, "INSERT INTO secondary (val) VALUES (1)"); err != nil {
		t.Fatalf("insert indexed auto column: %v", err)
	}
	if _, err := executeStatement(executor, "SELECT id FROM secondary"); err != nil {
		t.Fatalf("select indexed auto column: %v", err)
	}
}
