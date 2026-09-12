package mysql

import "testing"

func TestIssue381DatabaseAndSchemaReturnNullWithoutSelectedDatabase(t *testing.T) {
	executor := expressionExecutor(t)
	executor.session.database = ""
	executor.session.initialDB = ""

	cases := []struct {
		query    string
		want     string
		wantNull bool
	}{
		{query: "SELECT DATABASE()", wantNull: true},
		{query: "SELECT SCHEMA()", wantNull: true},
		{query: "SELECT DATABASE() IS NULL", want: "1"},
		{query: "SELECT SCHEMA() IS NULL", want: "1"},
		{query: "SELECT DATABASE() IS NOT NULL", want: "0"},
		{query: "SELECT SCHEMA() IS NOT NULL", want: "0"},
		{query: "SELECT DATABASE() <=> NULL", want: "1"},
		{query: "SELECT SCHEMA() <=> NULL", want: "1"},
		{query: "SELECT IFNULL(DATABASE(), 'fallback')", want: "fallback"},
		{query: "SELECT IFNULL(SCHEMA(), 'fallback')", want: "fallback"},
		{query: "SELECT COALESCE(DATABASE(), 'fallback')", want: "fallback"},
		{query: "SELECT COALESCE(SCHEMA(), 'fallback')", want: "fallback"},
		{query: "SELECT LENGTH(DATABASE())", wantNull: true},
		{query: "SELECT LENGTH(SCHEMA())", wantNull: true},
	}
	for _, test := range cases {
		result, err := executeStatement(executor, test.query)
		if err != nil {
			t.Fatalf("execute(%q) error: %v", test.query, err)
		}
		if len(result.rows) != 1 || len(result.rows[0]) != 1 || len(result.nulls) != 1 || len(result.nulls[0]) != 1 {
			t.Fatalf("execute(%q) result shape = %#v, want one value", test.query, result)
		}
		if result.nulls[0][0] != test.wantNull {
			t.Errorf("execute(%q) NULL flag = %v, want %v", test.query, result.nulls[0][0], test.wantNull)
		}
		if !test.wantNull && result.rows[0][0] != test.want {
			t.Errorf("execute(%q) = %q, want %q", test.query, result.rows[0][0], test.want)
		}
	}

	for _, query := range []string{"SELECT DATABASE()", "SELECT SCHEMA()"} {
		executor.session.database = "app"
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Fatalf("execute(%q) with selected database error: %v", query, err)
		}
		if len(result.rows) != 1 || len(result.rows[0]) != 1 || len(result.nulls) != 1 || len(result.nulls[0]) != 1 || result.nulls[0][0] || result.rows[0][0] != "app" {
			t.Errorf("execute(%q) with selected database = %#v, want app", query, result)
		}
	}
}
