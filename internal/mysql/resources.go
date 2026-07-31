package mysql

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ResourceUsage is the non-sensitive, server-wide evidence that diagnostics
// publishes. Current values fall when statements finish; peak and event
// counters are monotonic for the life of the server.
type ResourceUsage struct {
	ExecutionMemoryBytes      int64
	PeakExecutionMemoryBytes  int64
	TemporaryStorageBytes     int64
	PeakTemporaryStorageBytes int64
	SpillCount                int64
	SpillBytes                int64
	CancellationCount         int64
	TimeoutCount              int64
	MemoryExhaustionCount     int64
	TemporaryExhaustionCount  int64
}

type resourceLimits struct {
	memory    int64
	temporary int64
}

type resourceState struct {
	memory        int64
	peakMemory    int64
	temporary     int64
	peakTemporary int64
	spillCount    int64
	spillBytes    int64
}

// resourceManager owns server-wide resource ceilings that several concurrent
// statements share. A statement keeps its own usage in statementResources,
// then asks this manager to reserve the aggregate portion atomically.
type resourceManager struct {
	mu     sync.Mutex
	limits resourceLimits
	state  resourceState
	events resourceEvents
}

type resourceEvents struct {
	cancellations        int64
	timeouts             int64
	memoryExhaustions    int64
	temporaryExhaustions int64
}

func newResourceManager(config Config) *resourceManager {
	return &resourceManager{limits: resourceLimits{
		memory:    config.ResourceLimits.AggregateExecutionMemoryLimitBytes,
		temporary: config.ResourceLimits.AggregateTemporaryStorageLimitBytes,
	}}
}

func (m *resourceManager) reserveMemory(bytes int64) error {
	if bytes <= 0 || m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.limits.memory > 0 && m.state.memory+bytes > m.limits.memory {
		return aggregateExecutionMemoryLimitFailure()
	}
	m.state.memory += bytes
	if m.state.memory > m.state.peakMemory {
		m.state.peakMemory = m.state.memory
	}
	return nil
}

func (m *resourceManager) tryReserveMemory(bytes int64) bool {
	if bytes <= 0 || m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.limits.memory > 0 && m.state.memory+bytes > m.limits.memory {
		return false
	}
	m.state.memory += bytes
	if m.state.memory > m.state.peakMemory {
		m.state.peakMemory = m.state.memory
	}
	return true
}

func (m *resourceManager) releaseMemory(bytes int64) {
	if bytes <= 0 || m == nil {
		return
	}
	m.mu.Lock()
	m.state.memory -= bytes
	if m.state.memory < 0 {
		m.state.memory = 0
	}
	m.mu.Unlock()
}

func (m *resourceManager) reserveTemporary(bytes int64) error {
	if bytes <= 0 || m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.limits.temporary > 0 && m.state.temporary+bytes > m.limits.temporary {
		return aggregateTemporaryStorageLimitFailure()
	}
	m.state.temporary += bytes
	if m.state.temporary > m.state.peakTemporary {
		m.state.peakTemporary = m.state.temporary
	}
	return nil
}

func (m *resourceManager) releaseTemporary(bytes int64) {
	if bytes <= 0 || m == nil {
		return
	}
	m.mu.Lock()
	m.state.temporary -= bytes
	if m.state.temporary < 0 {
		m.state.temporary = 0
	}
	m.mu.Unlock()
}

func (m *resourceManager) recordSpill(bytes int64) {
	if m == nil || bytes <= 0 {
		return
	}
	m.mu.Lock()
	m.state.spillCount++
	m.state.spillBytes += bytes
	m.mu.Unlock()
}

func (m *resourceManager) recordFailure(kind string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	switch kind {
	case "statement_cancelled":
		m.events.cancellations++
	case "statement_timeout":
		m.events.timeouts++
	case "execution_memory_exhausted":
		m.events.memoryExhaustions++
	case "temporary_storage_exhausted":
		m.events.temporaryExhaustions++
	}
	m.mu.Unlock()
}

func (m *resourceManager) usage() ResourceUsage {
	if m == nil {
		return ResourceUsage{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return ResourceUsage{
		ExecutionMemoryBytes:      m.state.memory,
		PeakExecutionMemoryBytes:  m.state.peakMemory,
		TemporaryStorageBytes:     m.state.temporary,
		PeakTemporaryStorageBytes: m.state.peakTemporary,
		SpillCount:                m.state.spillCount,
		SpillBytes:                m.state.spillBytes,
		CancellationCount:         m.events.cancellations,
		TimeoutCount:              m.events.timeouts,
		MemoryExhaustionCount:     m.events.memoryExhaustions,
		TemporaryExhaustionCount:  m.events.temporaryExhaustions,
	}
}

// statementResources is the resource lifetime of one dispatched statement.
// It is created after command admission and is released before the next client
// command is accepted, so a failed statement cannot leak aggregate capacity.
type statementResources struct {
	mu        sync.Mutex
	manager   *resourceManager
	cancel    <-chan struct{}
	deadline  time.Time
	limits    resourceLimits
	state     resourceState
	failure   string
	finalized bool
}

func newStatementResources(manager *resourceManager, config Config, cancel <-chan struct{}) *statementResources {
	resources := &statementResources{
		manager: manager, cancel: cancel,
		limits: resourceLimits{memory: config.ResourceLimits.ExecutionMemoryLimitBytes, temporary: config.ResourceLimits.TemporaryStorageLimitBytes},
	}
	if config.ResourceLimits.StatementTimeout > 0 {
		resources.deadline = time.Now().Add(config.ResourceLimits.StatementTimeout)
	}
	return resources
}

func (r *statementResources) check() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checkLocked()
}

func (r *statementResources) deadlineAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deadline
}

func (r *statementResources) checkLocked() error {
	if r.finalized {
		return nil
	}
	if r.cancel != nil {
		select {
		case <-r.cancel:
			return r.failLocked(queryCancelledFailure())
		default:
		}
	}
	if !r.deadline.IsZero() && !time.Now().Before(r.deadline) {
		return r.failLocked(statementTimeoutFailure())
	}
	return nil
}

func reserveStatementMemory(r *statementResources, bytes int64) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkLocked(); err != nil {
		return err
	}
	if bytes <= 0 {
		return nil
	}
	if r.limits.memory > 0 && r.state.memory+bytes > r.limits.memory {
		return r.failLocked(executionMemoryLimitFailure())
	}
	if err := r.manager.reserveMemory(bytes); err != nil {
		return r.failLocked(err)
	}
	r.state.memory += bytes
	if r.state.memory > r.state.peakMemory {
		r.state.peakMemory = r.state.memory
	}
	return nil
}

func tryReserveStatementMemory(r *statementResources, bytes int64) (bool, error) {
	if r == nil {
		return true, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkLocked(); err != nil {
		return false, err
	}
	if bytes <= 0 {
		return true, nil
	}
	if r.limits.memory > 0 && r.state.memory+bytes > r.limits.memory {
		return false, nil
	}
	if !r.manager.tryReserveMemory(bytes) {
		return false, nil
	}
	r.state.memory += bytes
	if r.state.memory > r.state.peakMemory {
		r.state.peakMemory = r.state.memory
	}
	return true, nil
}

// observeMemory records the current size of one retained working set. It only
// reserves the growth above the previous high-water mark, which makes a chain
// of reshaping operators pay for its peak rather than the sum of each stage.
func observeStatementMemory(r *statementResources, bytes int64) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkLocked(); err != nil {
		return err
	}
	if bytes <= r.state.memory {
		return nil
	}
	return reserveStatementMemoryLocked(r, bytes-r.state.memory)
}

func reserveStatementMemoryLocked(r *statementResources, bytes int64) error {
	if r.limits.memory > 0 && r.state.memory+bytes > r.limits.memory {
		return r.failLocked(executionMemoryLimitFailure())
	}
	if err := r.manager.reserveMemory(bytes); err != nil {
		return r.failLocked(err)
	}
	r.state.memory += bytes
	if r.state.memory > r.state.peakMemory {
		r.state.peakMemory = r.state.memory
	}
	return nil
}

func releaseStatementMemory(r *statementResources, bytes int64) {
	if r == nil || bytes <= 0 {
		return
	}
	r.mu.Lock()
	if bytes > r.state.memory {
		bytes = r.state.memory
	}
	r.state.memory -= bytes
	r.mu.Unlock()
	r.manager.releaseMemory(bytes)
}

func reserveStatementTemporary(r *statementResources, bytes int64) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkLocked(); err != nil {
		return err
	}
	if bytes <= 0 {
		return nil
	}
	if r.limits.temporary > 0 && r.state.temporary+bytes > r.limits.temporary {
		return r.failLocked(temporaryStorageLimitFailure())
	}
	if err := r.manager.reserveTemporary(bytes); err != nil {
		return r.failLocked(err)
	}
	r.state.temporary += bytes
	if r.state.temporary > r.state.peakTemporary {
		r.state.peakTemporary = r.state.temporary
	}
	return nil
}

func releaseStatementTemporary(r *statementResources, bytes int64) {
	if r == nil || bytes <= 0 {
		return
	}
	r.mu.Lock()
	if bytes > r.state.temporary {
		bytes = r.state.temporary
	}
	r.state.temporary -= bytes
	r.mu.Unlock()
	r.manager.releaseTemporary(bytes)
}

func recordStatementSpill(r *statementResources, bytes int64) {
	if r == nil || bytes <= 0 {
		return
	}
	r.mu.Lock()
	r.state.spillCount++
	r.state.spillBytes += bytes
	r.mu.Unlock()
	r.manager.recordSpill(bytes)
}

func statementResourceSnapshot(r *statementResources) resourceStateSnapshot {
	if r == nil {
		return resourceStateSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return resourceStateSnapshot{
		peakMemory: r.state.peakMemory, temporary: r.state.peakTemporary,
		spillCount: r.state.spillCount, spillBytes: r.state.spillBytes, failure: r.failure,
	}
}

func (r *statementResources) failLocked(err error) error {
	kind := resourceFailureKind(err)
	if r.failure == "" && kind != "" {
		r.failure = kind
		r.manager.recordFailure(kind)
	}
	return err
}

type resourceStateSnapshot struct {
	peakMemory int64
	temporary  int64
	spillCount int64
	spillBytes int64
	failure    string
}

func resourceFailureKind(err error) string {
	var failure sqlFailure
	if !errors.As(err, &failure) {
		return ""
	}
	switch failure.code {
	case 1317:
		return "statement_cancelled"
	case 3024:
		return "statement_timeout"
	case 1114:
		if strings.Contains(failure.message, "temporary") {
			return "temporary_storage_exhausted"
		}
		return "execution_memory_exhausted"
	default:
		return ""
	}
}

func closeStatementResources(r *statementResources) {
	if r == nil {
		return
	}
	r.mu.Lock()
	memory, temporary := r.state.memory, r.state.temporary
	r.state.memory, r.state.temporary = 0, 0
	r.mu.Unlock()
	r.manager.releaseMemory(memory)
	r.manager.releaseTemporary(temporary)
}

// finalizeStatementResources marks a successfully published commit as the
// statement's final observable boundary. A cancellation or deadline arriving
// after that point cannot retroactively reject durable work.
func finalizeStatementResources(r *statementResources) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.finalized = true
	r.mu.Unlock()
}

func (s *session) checkStatementResources() error {
	if s == nil {
		return nil
	}
	return s.resources.check()
}

func (s *session) observeBufferedMemory(bytes int) error {
	if s == nil {
		return nil
	}
	return observeStatementMemory(s.resources, int64(bytes))
}

func (s *session) reserveDeliveredRow(bytes int) (func(), error) {
	if s == nil || s.resources == nil || bytes <= 0 {
		return func() {}, nil
	}
	if err := reserveStatementMemory(s.resources, int64(bytes)); err != nil {
		return nil, err
	}
	return func() { releaseStatementMemory(s.resources, int64(bytes)) }, nil
}

func (s *session) resourceSnapshot() resourceStateSnapshot {
	if s == nil {
		return resourceStateSnapshot{}
	}
	return statementResourceSnapshot(s.resources)
}

func statementTimeoutFailure() error {
	return sqlFailure{3024, "HY000", "Query execution was interrupted, maximum statement execution time exceeded"}
}

func executionMemoryLimitFailure() error {
	return sqlFailure{1114, "HY000", "execution memory limit exceeded"}
}

func aggregateExecutionMemoryLimitFailure() error {
	return sqlFailure{1114, "HY000", "aggregate execution memory limit exceeded"}
}

func temporaryStorageLimitFailure() error {
	return sqlFailure{1114, "HY000", "temporary storage limit exceeded"}
}

func aggregateTemporaryStorageLimitFailure() error {
	return sqlFailure{1114, "HY000", "aggregate temporary storage limit exceeded"}
}
