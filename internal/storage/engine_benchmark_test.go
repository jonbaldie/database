package storage_test

import (
	"strconv"
	"testing"

	"github.com/jonbaldie/database/internal/storage"
)

func BenchmarkEngineStartupAfterWrites(b *testing.B) {
	for _, writes := range []int{64, 256, 1024} {
		b.Run("writes="+strconv.Itoa(writes), func(b *testing.B) {
			directory := b.TempDir()
			engine, err := storage.Open(directory)
			if err != nil {
				b.Fatal(err)
			}
			if err := engine.EnsureTable("app", "items", []string{"id", "value"}, []string{"id"}, nil); err != nil {
				b.Fatal(err)
			}
			seed, err := engine.Begin()
			if err != nil {
				b.Fatal(err)
			}
			if err := seed.Insert("app", "items", []string{"1", "0"}); err != nil {
				b.Fatal(err)
			}
			if err := seed.Commit(); err != nil {
				b.Fatal(err)
			}
			for id := range writes {
				txn, beginErr := engine.Begin()
				if beginErr != nil {
					b.Fatal(beginErr)
				}
				if err := txn.UpdatePrimary("app", "items", "1", []string{"1", strconv.Itoa(id)}); err != nil {
					b.Fatal(err)
				}
				if err := txn.Commit(); err != nil {
					b.Fatal(err)
				}
			}
			if err := engine.Close(); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				reopened, openErr := storage.Open(directory)
				if openErr != nil {
					b.Fatal(openErr)
				}
				if got := reopened.RowCount("app", "items"); got != 1 {
					b.Fatalf("row count = %d, want 1 after %d writes", got, writes)
				}
				if err := reopened.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

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
