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
