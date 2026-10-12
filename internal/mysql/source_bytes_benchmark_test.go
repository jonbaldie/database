package mysql

import (
	"strconv"
	"testing"
)

func BenchmarkSourceRowBytesRangeCorpus(b *testing.B) {
	rows := make([][]string, 100000)
	for index := range rows {
		key := strconv.Itoa(index)
		rows[index] = []string{key, key, "task-" + key}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if got := sourceRowBytes(rows); got != 1966670 {
			b.Fatalf("source bytes = %d, want 1966670", got)
		}
	}
}
