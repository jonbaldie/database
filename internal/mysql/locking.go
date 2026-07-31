package mysql

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type lockMode uint8

const (
	lockShared lockMode = iota + 1
	lockExclusive
)

type lockPolicy uint8

const (
	lockWait lockPolicy = iota
	lockNoWait
	lockSkip
)

// rowLockResource identifies one stable row value image in a table snapshot.
type rowLockResource struct {
	namespace string
	table     string
	key       string
}

type lockSnapshot map[rowLockResource]lockMode

// lockManager owns all row locks for one server. It uses a wait graph to
// detect cycles before a blocked statement starts to wait.
type lockManager struct {
	mu      sync.Mutex
	timeout time.Duration
	granted map[rowLockResource]map[*session]lockMode
	held    map[*session]map[rowLockResource]lockMode
	waiting map[*session]map[*session]struct{}
	changed chan struct{}
}

func newLockManager(timeout time.Duration) *lockManager {
	return &lockManager{
		timeout: timeout,
		granted: make(map[rowLockResource]map[*session]lockMode),
		held:    make(map[*session]map[rowLockResource]lockMode),
		waiting: make(map[*session]map[*session]struct{}),
		changed: make(chan struct{}),
	}
}

func (m *lockManager) acquire(waiter *session, resources []rowLockResource, mode lockMode, policy lockPolicy) (bool, error) {
	resources = uniqueLockResources(resources)
	if len(resources) == 0 {
		return true, nil
	}
	switch policy {
	case lockSkip:
		return m.skipLocked(waiter, resources, mode)
	case lockNoWait:
		return lockNowait(m, waiter, resources, mode)
	default:
		return m.waitForLock(waiter, resources, mode)
	}
}

func (m *lockManager) skipLocked(waiter *session, resources []rowLockResource, mode lockMode) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.conflictingOwners(waiter, resources, mode)) > 0 {
		return false, nil
	}
	m.grant(waiter, resources, mode)
	return true, nil
}

// lockResourcesAvailable reports whether the caller can take all resources
// now. It does not grant a lock. SKIP LOCKED uses it to omit offset rows
// without locking rows that are outside the returned result window.
func lockResourcesAvailable(m *lockManager, requester *session, resources []rowLockResource, mode lockMode) bool {
	resources = uniqueLockResources(resources)
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conflictingOwners(requester, resources, mode)) == 0
}

func lockNowait(m *lockManager, waiter *session, resources []rowLockResource, mode lockMode) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.conflictingOwners(waiter, resources, mode)) > 0 {
		return false, lockNowaitFailure()
	}
	m.grant(waiter, resources, mode)
	return true, nil
}

func (m *lockManager) waitForLock(waiter *session, resources []rowLockResource, mode lockMode) (bool, error) {
	deadline := time.Now().Add(m.timeout)
	for {
		changed, acquired, err := m.waitAttempt(waiter, resources, mode)
		if err != nil {
			return false, err
		}
		if acquired {
			return true, nil
		}
		if err := waitForLockChange(changed, waiter.statementCancel, deadline); err != nil {
			m.stopWaiting(waiter)
			return false, err
		}
	}
}

func (m *lockManager) waitAttempt(waiter *session, resources []rowLockResource, mode lockMode) (<-chan struct{}, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owners := m.conflictingOwners(waiter, resources, mode)
	if len(owners) == 0 {
		delete(m.waiting, waiter)
		m.grant(waiter, resources, mode)
		return nil, true, nil
	}
	m.waiting[waiter] = owners
	if m.waitGraphReaches(owners, waiter, map[*session]bool{}) {
		delete(m.waiting, waiter)
		return nil, false, deadlockFailure()
	}
	return m.changed, false, nil
}

func waitForLockChange(changed, cancelled <-chan struct{}, deadline time.Time) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return lockWaitTimeoutFailure()
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-changed:
		return nil
	case <-timer.C:
		return lockWaitTimeoutFailure()
	case <-cancelled:
		return queryCancelledFailure()
	}
}

func (m *lockManager) stopWaiting(session *session) {
	m.mu.Lock()
	delete(m.waiting, session)
	m.mu.Unlock()
}

func (m *lockManager) conflictingOwners(requester *session, resources []rowLockResource, wanted lockMode) map[*session]struct{} {
	owners := make(map[*session]struct{})
	for heldResource, holders := range m.granted {
		for _, requestedResource := range resources {
			if !lockResourcesOverlap(heldResource, requestedResource) {
				continue
			}
			for holder, heldMode := range holders {
				if holder != requester && (wanted == lockExclusive || heldMode == lockExclusive) {
					owners[holder] = struct{}{}
				}
			}
		}
	}
	return owners
}

func lockResourcesOverlap(left, right rowLockResource) bool {
	if left.namespace != right.namespace || left.table != right.table {
		return false
	}
	return left.key == right.key
}

func (m *lockManager) waitGraphReaches(owners map[*session]struct{}, target *session, visited map[*session]bool) bool {
	for owner := range owners {
		if owner == target {
			return true
		}
		if visited[owner] {
			continue
		}
		visited[owner] = true
		if m.waitGraphReaches(m.waiting[owner], target, visited) {
			return true
		}
	}
	return false
}

func (m *lockManager) grant(owner *session, resources []rowLockResource, mode lockMode) {
	for _, resource := range resources {
		holders := m.granted[resource]
		if holders == nil {
			holders = make(map[*session]lockMode)
			m.granted[resource] = holders
		}
		if mode == lockExclusive || holders[owner] == 0 {
			holders[owner] = mode
		}
		held := m.held[owner]
		if held == nil {
			held = make(map[rowLockResource]lockMode)
			m.held[owner] = held
		}
		if mode == lockExclusive || held[resource] == 0 {
			held[resource] = mode
		}
	}
}

func (m *lockManager) release(session *session) {
	m.mu.Lock()
	for resource := range m.held[session] {
		holders := m.granted[resource]
		delete(holders, session)
		if len(holders) == 0 {
			delete(m.granted, resource)
		}
	}
	delete(m.held, session)
	delete(m.waiting, session)
	m.signal()
	m.mu.Unlock()
}

func (m *lockManager) snapshot(owner *session) lockSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneHeldLocks(m.held[owner])
}

func (m *lockManager) restore(owner *session, snapshot lockSnapshot) {
	m.mu.Lock()
	for resource := range m.held[owner] {
		if _, heldBefore := snapshot[resource]; heldBefore {
			continue
		}
		holders := m.granted[resource]
		delete(holders, owner)
		if len(holders) == 0 {
			delete(m.granted, resource)
		}
	}
	if len(snapshot) == 0 {
		delete(m.held, owner)
	} else {
		m.held[owner] = cloneHeldLocks(snapshot)
		for resource, mode := range snapshot {
			holders := m.granted[resource]
			if holders == nil {
				holders = make(map[*session]lockMode)
				m.granted[resource] = holders
			}
			holders[owner] = mode
		}
	}
	m.signal()
	m.mu.Unlock()
}

func cloneHeldLocks(held map[rowLockResource]lockMode) lockSnapshot {
	copy := make(lockSnapshot, len(held))
	for resource, mode := range held {
		copy[resource] = mode
	}
	return copy
}

func (m *lockManager) signal() {
	close(m.changed)
	m.changed = make(chan struct{})
}

func uniqueLockResources(resources []rowLockResource) []rowLockResource {
	if len(resources) < 2 {
		return resources
	}
	unique := append([]rowLockResource(nil), resources...)
	sort.Slice(unique, func(left, right int) bool {
		if unique[left].namespace != unique[right].namespace {
			return unique[left].namespace < unique[right].namespace
		}
		if unique[left].table != unique[right].table {
			return unique[left].table < unique[right].table
		}
		return unique[left].key < unique[right].key
	})
	result := unique[:0]
	for _, resource := range unique {
		if len(result) == 0 || result[len(result)-1] != resource {
			result = append(result, resource)
		}
	}
	return result
}

func lockNowaitFailure() error {
	return sqlFailure{3572, "HY000", "Do not wait for lock"}
}

func lockWaitTimeoutFailure() error {
	return sqlFailure{1205, "HY000", "Lock wait timeout exceeded; try restarting transaction"}
}

func deadlockFailure() error {
	return sqlFailure{1213, "40001", "Deadlock found when trying to get lock; try restarting transaction"}
}

func queryCancelledFailure() error {
	return sqlFailure{1317, "70100", "Query execution was interrupted"}
}

func (s *relationExecutor) acquireWriteLocks(resources []rowLockResource) error {
	_, err := s.session.server.locks.acquire(s.session, resources, lockExclusive, lockWait)
	return err
}

func matchingRowLocks(namespace, table string, rows [][]string, matcher func([]string) bool) []rowLockResource {
	resources := make([]rowLockResource, 0)
	for _, row := range rows {
		if matcher(row) {
			resources = append(resources, rowLockResource{namespace: namespace, table: table, key: rowLockKey(row)})
		}
	}
	return resources
}

func rowLockKey(row []string) string {
	var builder strings.Builder
	for _, value := range row {
		builder.WriteString(strconv.Itoa(len(value)))
		builder.WriteByte(':')
		builder.WriteString(value)
	}
	return builder.String()
}
