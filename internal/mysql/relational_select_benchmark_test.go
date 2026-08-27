package mysql

import (
	"strconv"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func BenchmarkRelationalEqualityJoin(b *testing.B) {
	for _, rows := range []int{100, 500, 1000} {
		b.Run("rows="+strconv.Itoa(rows), func(b *testing.B) {
			executor := relationalJoinBenchmarkExecutor(b, rows)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				result, err := executeStatement(executor, "SELECT join_left.id FROM join_left JOIN join_right ON join_left.id = join_right.id")
				if err != nil {
					b.Fatal(err)
				}
				if len(result.rows) != rows {
					b.Fatalf("joined rows = %d, want %d", len(result.rows), rows)
				}
			}
		})
	}
}

func BenchmarkWindowFunctionsAfterPartitionSort(b *testing.B) {
	for _, rowCount := range []int{100, 400, 800} {
		b.Run("rows="+strconv.Itoa(rowCount), func(b *testing.B) {
			plan, rows, partition, functions := windowFunctionBenchmark(rowCount)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				cache := make(map[string][][]int, len(functions))
				for _, function := range functions {
					cache[relationalWindowSpecKey(function.spec)] = [][]int{partition}
					values, err := plan.windowFunctionValues(rows, function, cache)
					if err != nil {
						b.Fatal(err)
					}
					if len(values) != rowCount {
						b.Fatalf("window values = %d, want %d", len(values), rowCount)
					}
				}
			}
		})
	}
}

func windowFunctionBenchmark(rowCount int) (*relationalSelectPlan, []relationalResultRow, []int, []relationalWindowFunction) {
	plan := &relationalSelectPlan{source: relationalSource{columns: []relationColumn{{name: "value", typeName: "INT", index: 0}}}}
	rows := make([]relationalResultRow, rowCount)
	partition := make([]int, rowCount)
	for row := range rowCount {
		rows[row] = relationalResultRow{source: relationRow{values: []string{strconv.Itoa(row / 2)}}}
		partition[row] = row
	}
	order := []relationalWindowOrder{{expression: "value", direction: "ASC"}}
	wholePartition := relationalWindowFrame{
		present: true,
		mode:    "rows",
		start:   relationalWindowBound{kind: "unbounded_preceding"},
		end:     relationalWindowBound{kind: "unbounded_following"},
	}
	numericRange := relationalWindowFrame{
		present: true,
		mode:    "range",
		start:   relationalWindowBound{kind: "preceding", offset: 10},
		end:     relationalWindowBound{kind: "current_row"},
	}
	return plan, rows, partition, []relationalWindowFunction{
		{relationalAggregate: relationalAggregate{name: "RANK"}, spec: relationalWindowSpec{order: order}},
		{relationalAggregate: relationalAggregate{name: "SUM", argument: "value"}, spec: relationalWindowSpec{order: order, frame: wholePartition}},
		{relationalAggregate: relationalAggregate{name: "COUNT", argument: "*"}, spec: relationalWindowSpec{order: order, frame: numericRange}},
	}
}

func relationalJoinBenchmarkExecutor(b *testing.B, rows int) *textStatementExecutor {
	b.Helper()
	store, err := catalog.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	if err := store.CreateNamespace("benchmark"); err != nil {
		b.Fatal(err)
	}
	for _, table := range []string{"join_left", "join_right"} {
		if err := store.CreateTableWithTypes("benchmark", table, []string{"id"}, []string{"INT"}); err != nil {
			b.Fatal(err)
		}
		for row := range rows {
			if err := store.Insert("benchmark", table, []string{strconv.Itoa(row)}); err != nil {
				b.Fatal(err)
			}
		}
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0"})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = server.Listener.Close() })
	session := &session{server: server, database: "benchmark", initialDB: "benchmark", timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{}}
	return &textStatementExecutor{session: session}
}
