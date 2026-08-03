package mysql

import (
	"sync"
	"time"

	"github.com/jonbaldie/database/internal/queryexplanation"
)

// activeExplanationRegistry keeps the immutable plan that one active session
// is executing. Capturing a snapshot copies this plan and never waits for or
// changes the observed statement.
type activeExplanationRegistry struct {
	mu     sync.RWMutex
	active map[uint32]*activeExplanation
}

type activeExplanation struct {
	plan    *queryexplanation.Document
	started time.Time
	session *session
	metrics *queryexplanation.RuntimeMetrics
}

func newActiveExplanationRegistry() *activeExplanationRegistry {
	return &activeExplanationRegistry{active: make(map[uint32]*activeExplanation)}
}

func (r *activeExplanationRegistry) begin(connectionID uint32, plan *queryexplanation.Document, session *session) func() {
	if r == nil || connectionID == 0 || plan == nil {
		return func() {}
	}
	entry := &activeExplanation{plan: plan, started: time.Now(), session: session, metrics: queryexplanation.NewRuntimeMetrics(plan)}
	r.mu.Lock()
	r.active[connectionID] = entry
	r.mu.Unlock()
	// Do not attach entry.metrics to session.runtimeMetrics. That flag is reserved
	// for EXPLAIN ANALYZE, which must walk full operators; prepared-statement live
	// explanation must keep the point-lookup fast path.
	return func() {
		r.mu.Lock()
		if r.active[connectionID] == entry {
			delete(r.active, connectionID)
		}
		r.mu.Unlock()
	}
}

func (r *activeExplanationRegistry) snapshot(connectionID uint32) (*queryexplanation.Document, bool) {
	if r == nil || connectionID == 0 {
		return nil, false
	}
	r.mu.RLock()
	entry := r.active[connectionID]
	r.mu.RUnlock()
	if entry == nil {
		return nil, false
	}
	now := time.Now()
	lockWait := time.Duration(0)
	if entry.session != nil {
		lockWait = lockWaitDuration(entry.session.server.locks, entry.session, now)
		recordRuntimeResources(entry.metrics, entry.session.resourceSnapshot())
	}
	return queryexplanation.SnapshotWithMetrics(entry.plan, connectionID, now, now.Sub(entry.started), lockWait, entry.metrics), true
}

func recordRuntimeResources(metrics *queryexplanation.RuntimeMetrics, usage resourceStateSnapshot) {
	metrics.RecordRootResources(int(usage.peakMemory), int(usage.spillCount), int(usage.spillBytes), int(usage.temporary), usage.failure)
}
