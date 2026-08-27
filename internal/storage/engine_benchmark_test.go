package storage_test

import (
	"strconv"
	"testing"

	"github.com/jonbaldie/database/internal/storage"
)

func BenchmarkEnginePointUpdate(b *testing.B) {
	for _, rows := range []int{100, 500, 1000} {
		b.Run("rows="+strconv.Itoa(rows), func(b *testing.B) {
			engine := pointUpdateBenchmarkEngine(b, rows)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				txn, err := engine.Begin()
				if err != nil {
					b.Fatal(err)
				}
				if err := txn.UpdatePrimary("app", "items", "0", []string{"0", "changed"}); err != nil {
					b.Fatal(err)
				}
				if err := txn.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTransactionStageBatchInsert(b *testing.B) {
	for _, rows := range []int{100, 400, 800} {
		b.Run("rows="+strconv.Itoa(rows), func(b *testing.B) {
			engine, err := storage.Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = engine.Close() })
			if err := engine.EnsureTable("app", "items", []string{"id", "code"}, []string{"id"}, [][]string{{"code"}}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				txn, beginErr := engine.Begin()
				if beginErr != nil {
					b.Fatal(beginErr)
				}
				for row := range rows {
					value := strconv.Itoa(row)
					if err := txn.Insert("app", "items", []string{value, "code-" + value}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func pointUpdateBenchmarkEngine(b *testing.B, rows int) *storage.Engine {
	b.Helper()
	engine, err := storage.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = engine.Close() })
	if err := engine.EnsureTable("app", "items", []string{"id", "value"}, []string{"id"}, nil); err != nil {
		b.Fatal(err)
	}
	txn, err := engine.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for id := range rows {
		value := strconv.Itoa(id)
		if err := txn.Insert("app", "items", []string{value, value}); err != nil {
			b.Fatal(err)
		}
	}
	if err := txn.Commit(); err != nil {
		b.Fatal(err)
	}
	return engine
}
