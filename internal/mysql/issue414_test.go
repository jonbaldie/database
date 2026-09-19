package mysql

import "testing"

func TestIssue414TypeCoalescingFunctionsAdvertiseMergedType(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, n INT, d DOUBLE, u INT UNSIGNED, m DECIMAL(5,2), day DATE)",
		"INSERT INTO t VALUES (1, NULL, 2.5, 7, 3.25, NULL)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	for _, test := range []struct {
		query string
		want  string
		typ   byte
	}{
		{query: "SELECT IFNULL(id, 'none') FROM t", want: "1", typ: mysqlTypeVarString},
		{query: "SELECT IFNULL(n, 'none') FROM t", want: "none", typ: mysqlTypeVarString},
		{query: "SELECT IFNULL(MAX(id), 'none') FROM t WHERE 1=0", want: "none", typ: mysqlTypeVarString},
		{query: "SELECT COALESCE(id, 'none') FROM t", want: "1", typ: mysqlTypeVarString},
		{query: "SELECT COALESCE(n, 'none') FROM t", want: "none", typ: mysqlTypeVarString},
		{query: "SELECT COALESCE(n, id, 'none') FROM t", want: "1", typ: mysqlTypeVarString},
		{query: "SELECT COALESCE(MAX(id), 'none') FROM t WHERE 1=0", want: "none", typ: mysqlTypeVarString},
		{query: "SELECT COALESCE(n, 'none', id) FROM t", want: "none", typ: mysqlTypeVarString},
		{query: "SELECT IFNULL(n, d) FROM t", want: "2.5", typ: mysqlTypeDouble},
		{query: "SELECT COALESCE(n, d) FROM t", want: "2.5", typ: mysqlTypeDouble},
		{query: "SELECT IFNULL(id, n) FROM t", want: "1", typ: mysqlTypeLongLong},
		{query: "SELECT COALESCE(n, id) FROM t", want: "1", typ: mysqlTypeLongLong},
		{query: "SELECT IFNULL(id, m) FROM t", want: "1.00", typ: mysqlTypeNewDecimal},
		{query: "SELECT COALESCE(id, u) FROM t", want: "1", typ: mysqlTypeNewDecimal},
		{query: "SELECT COALESCE(day, id) FROM t", want: "1", typ: mysqlTypeVarString},
		{query: "SELECT IFNULL(1, 2.5)", want: "1.0", typ: mysqlTypeNewDecimal},
		{query: "SELECT COALESCE(NULL, 1, 'x')", want: "1", typ: mysqlTypeVarString},
	} {
		result, err := executeStatement(executor, test.query)
		if err != nil || !equalRows(result.rows, [][]string{{test.want}}) {
			t.Errorf("%s rows = %#v, err = %v, want [[%s]]", test.query, result.rows, err, test.want)
			continue
		}
		if len(result.metadata) != 1 || result.metadata[0].typ != test.typ {
			t.Errorf("%s metadata = %#v, want type %d", test.query, result.metadata, test.typ)
			continue
		}
		if _, err := binaryRow(result.rows[0], 0, result.nulls, result.metadata); err != nil {
			t.Errorf("%s binary encode: %v", test.query, err)
		}
	}
}
