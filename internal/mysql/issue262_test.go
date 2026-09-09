package mysql

import "testing"

func TestIssue262StringLiteralsDecodeMySQLBackslashEscapes(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE strings (id INT PRIMARY KEY, value VARCHAR(64))",
		`INSERT INTO strings VALUES
			(1, 'a\\b'),
			(2, 'line1\nline2'),
			(3, 'a\0b'),
			(4, 'it\'s'),
			(5, 'a\"b'),
			(6, 'a\rb'),
			(7, 'a\tb'),
			(8, 'a\bb'),
			(9, 'a\Zb')`,
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id, value, LENGTH(value), CHAR_LENGTH(value) FROM strings ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"1", "a\\b", "3", "3"},
		{"2", "line1\nline2", "11", "11"},
		{"3", "a\x00b", "3", "3"},
		{"4", "it's", "4", "4"},
		{"5", "a\"b", "3", "3"},
		{"6", "a\rb", "3", "3"},
		{"7", "a\tb", "3", "3"},
		{"8", "a\bb", "3", "3"},
		{"9", "a\x1ab", "3", "3"},
	}
	if !equalRows(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}
}
