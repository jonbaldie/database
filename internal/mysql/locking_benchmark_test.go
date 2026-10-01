package mysql

import (
	"strconv"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
)

func BenchmarkLockManagerUncontendedRows(b *testing.B) {
	for _, rows := range []int{100, 400, 800} {
		b.Run("rows="+strconv.Itoa(rows), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				manager := newLockManager(time.Second)
				owner := &session{}
				table := catalog.NewTableRef("app", "items")
				for row := range rows {
					resource := rowLockResource{table: table, key: strconv.Itoa(row)}
					if acquired, err := manager.acquire(owner, []rowLockResource{resource}, lockExclusive, lockNoWait); err != nil || !acquired {
						b.Fatalf("lock row %d: acquired=%v err=%v", row, acquired, err)
					}
				}
				manager.release(owner)
			}
		})
	}
}
