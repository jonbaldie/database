package mysql

import "testing"

// TestIssue488DropIndexRemovesUniqueKey checks that DROP INDEX removes a
// unique key that a table definition declares, by the name SHOW INDEX lists.
func TestIssue488DropIndexRemovesUniqueKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create string
		drop   string
	}{
		{"server-named, ALTER TABLE", "CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))", "ALTER TABLE p DROP INDEX code"},
		{"server-named, DROP INDEX", "CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))", "DROP INDEX code ON p"},
		{"inline column UNIQUE", "CREATE TABLE p (id INT PRIMARY KEY, code INT UNIQUE)", "ALTER TABLE p DROP INDEX code"},
		{"user-named", "CREATE TABLE p (id INT PRIMARY KEY, code INT, CONSTRAINT uq_code UNIQUE (code))", "ALTER TABLE p DROP KEY UQ_CODE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range []string{tc.create, "INSERT INTO p VALUES (1, 10)"} {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("setup %q: %v", query, err)
				}
			}
			if _, err := executeStatement(executor, tc.drop); err != nil {
				t.Fatalf("%q: %v", tc.drop, err)
			}
			result, err := executeStatement(executor, "SHOW INDEX FROM p")
			if err != nil {
				t.Fatalf("SHOW INDEX: %v", err)
			}
			for _, row := range result.rows {
				if row[2] != "PRIMARY" {
					t.Fatalf("SHOW INDEX after %q lists %v", tc.drop, row)
				}
			}
			if _, err := executeStatement(executor, "INSERT INTO p VALUES (2, 10)"); err != nil {
				t.Fatalf("duplicate code after %q: %v", tc.drop, err)
			}
			if _, err := executeStatement(executor, tc.drop); err == nil {
				t.Fatalf("second %q succeeded", tc.drop)
			}
		})
	}
}

// TestIssue488DropIndexKeepsReferencedUniqueKey checks that DROP INDEX does
// not remove a unique key that a foreign key references.
func TestIssue488DropIndexKeepsReferencedUniqueKey(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE p (id INT PRIMARY KEY, code INT, UNIQUE (code))",
		"CREATE TABLE c (id INT PRIMARY KEY, pcode INT, CONSTRAINT fk1 FOREIGN KEY (pcode) REFERENCES p (code))",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	for _, drop := range []string{"ALTER TABLE p DROP INDEX code", "DROP INDEX code ON p"} {
		if _, err := executeStatement(executor, drop); err == nil {
			t.Fatalf("%q succeeded while fk1 references the key", drop)
		}
	}
	result, err := executeStatement(executor, "SHOW INDEX FROM p WHERE Key_name = 'code'")
	if err != nil {
		t.Fatalf("SHOW INDEX: %v", err)
	}
	if len(result.rows) != 1 {
		t.Fatalf("SHOW INDEX rows for code = %v, want one row", result.rows)
	}
}

// TestIssue488DropIndexKeepsCheckConstraint checks that DROP INDEX removes only
// unique keys from the constraint list, not a CHECK constraint of that name.
func TestIssue488DropIndexKeepsCheckConstraint(t *testing.T) {
	executor := ddlExecutorForTest(t)
	if _, err := executeStatement(executor, "CREATE TABLE p (id INT PRIMARY KEY, code INT, CONSTRAINT chk_code CHECK (code > 0))"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := executeStatement(executor, "ALTER TABLE p DROP INDEX chk_code"); err == nil {
		t.Fatal("ALTER TABLE p DROP INDEX chk_code succeeded")
	}
	if _, err := executeStatement(executor, "INSERT INTO p VALUES (1, 0)"); !isFailureCode(err, 3819) {
		t.Fatalf("INSERT violating chk_code: expected 3819, got %v", err)
	}
}
