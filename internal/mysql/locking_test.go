package mysql

import (
	"testing"
	"time"
)

func TestLockManagerUpgradeWaitsForOtherSharedOwner(t *testing.T) {
	manager := newLockManager(time.Second)
	first := &session{}
	second := &session{}
	resource := rowLockResource{namespace: "app", table: "items", key: "1"}
	for _, owner := range []*session{first, second} {
		acquired, err := manager.acquire(owner, []rowLockResource{resource}, lockShared, lockNoWait)
		if err != nil || !acquired {
			t.Fatalf("shared lock: acquired=%v err=%v", acquired, err)
		}
	}
	if acquired, err := manager.acquire(first, []rowLockResource{resource}, lockExclusive, lockNoWait); err == nil || acquired {
		t.Fatalf("conflicting upgrade: acquired=%v err=%v", acquired, err)
	}
	manager.release(second)
	if acquired, err := manager.acquire(first, []rowLockResource{resource}, lockExclusive, lockNoWait); err != nil || !acquired {
		t.Fatalf("released upgrade: acquired=%v err=%v", acquired, err)
	}
	if manager.granted[resource][first] != lockExclusive || manager.held[first][resource] != lockExclusive {
		t.Fatal("upgrade did not replace the shared lock")
	}
	manager.release(first)
	if len(manager.granted) != 0 || len(manager.held) != 0 {
		t.Fatalf("locks remain after release: granted=%d held=%d", len(manager.granted), len(manager.held))
	}
}
