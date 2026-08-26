package mysql

import "testing"

func TestTransactionControlAcceptsExtraKeywordWhitespace(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE items (id INT PRIMARY KEY)",
		"START  TRANSACTION",
		"INSERT INTO items VALUES (1)",
		"SAVEPOINT mark",
		"INSERT INTO items VALUES (2)",
		"ROLLBACK  TO mark",
		"SAVEPOINT keep",
		"RELEASE  SAVEPOINT keep",
		"COMMIT  WORK",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	result, err := executeStatement(executor, "SELECT id FROM items ORDER BY id")
	if err != nil || !equalRows(result.rows, [][]string{{"1"}}) {
		t.Fatalf("committed rows = %#v, err = %v", result, err)
	}
}
