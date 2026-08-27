package mysql

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkLockManagerUncontendedRows(b *testing.B) {
	for _, rows := range []int{100, 400, 800} {
		b.Run("rows="+strconv.Itoa(rows), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				manager := newLockManager(time.Second)
				owner := &session{}
				for row := range rows {
					resource := rowLockResource{namespace: "app", table: "items", key: strconv.Itoa(row)}
					if acquired, err := manager.acquire(owner, []rowLockResource{resource}, lockExclusive, lockNoWait); err != nil || !acquired {
						b.Fatalf("lock row %d: acquired=%v err=%v", row, acquired, err)
					}
				}
				manager.release(owner)
			}
		})
	}
}
