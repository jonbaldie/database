package main

import (
	"encoding/json"
	"testing"
)

func TestReturnsRowsRoutesResultSetStatementsToQuery(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                             true,
		"  select id FROM t":                   true,
		"(SELECT 1) UNION (SELECT 2)":          true,
		"/* hint */ SELECT 1":                  true,
		"-- note\nSHOW TABLES":                 true,
		"# note\nDESCRIBE t":                   true,
		"DESC t":                               true,
		"EXPLAIN SELECT 1":                     true,
		"WITH c AS (SELECT 1) SELECT * FROM c": true,
		"VALUES ROW(1)":                        true,
		"TABLE t":                              true,
		"INSERT INTO t VALUES (1,10),(2,20)":   false,
		"insert into t select * from u":        false,
		"UPDATE t SET qty = 1":                 false,
		"DELETE FROM t":                        false,
		"REPLACE INTO t VALUES (1,1)":          false,
		"CREATE TABLE t (id INT PRIMARY KEY)":  false,
		"USE shop":                             false,
		"SET autocommit = 0":                   false,
		"SELECTED":                             false,
		"":                                     false,
	}
	for statement, want := range cases {
		if got := returnsRows(statement); got != want {
			t.Errorf("returnsRows(%q) = %v, want %v", statement, got, want)
		}
	}
}

func TestOutcomeReportsMeasuredZeroButOmitsUnmeasuredCount(t *testing.T) {
	zero := int64(0)
	measured, _ := json.Marshal(outcome{Statement: "DELETE FROM t", OK: true, RowsAffected: &zero, LastInsertID: &zero})
	if want := `{"statement":"DELETE FROM t","ok":true,"rows_affected":0,"last_insert_id":0}`; string(measured) != want {
		t.Errorf("measured outcome = %s, want %s", measured, want)
	}
	unmeasured, _ := json.Marshal(outcome{Statement: "SELECT 1", OK: true, Columns: []string{"1"}, Rows: [][]string{{"1"}}})
	if want := `{"statement":"SELECT 1","ok":true,"columns":["1"],"rows":[["1"]]}`; string(unmeasured) != want {
		t.Errorf("unmeasured outcome = %s, want %s", unmeasured, want)
	}
}
