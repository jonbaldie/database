package catalog

import "time"

type writeRequest struct {
	apply            func(Definition) (Definition, error)
	expectedRevision uint64
	requireRevision  bool
	result           chan error
}

func (s *Store) ensureWriter() {
	s.writerOnce.Do(func() {
		s.writes = make(chan *writeRequest, 1024)
		go s.writeLoop()
	})
}

// ApplyDurable applies one isolated catalog mutation through the coalesced
// writer so concurrent commits can share a single durable sync.
func (s *Store) ApplyDurable(apply func(Definition) (Definition, error)) error {
	return s.applyDurable(false, 0, apply)
}

// ApplyDurableIfRevision is ApplyDurable that fails with ErrRevisionConflict
// when the catalog moved past expected before publication.
func (s *Store) ApplyDurableIfRevision(expected uint64, apply func(Definition) (Definition, error)) error {
	return s.applyDurable(true, expected, apply)
}

func (s *Store) applyDurable(requireRevision bool, expected uint64, apply func(Definition) (Definition, error)) error {
	s.ensureWriter()
	req := &writeRequest{apply: apply, expectedRevision: expected, requireRevision: requireRevision, result: make(chan error, 1)}
	s.writes <- req
	return <-req.result
}

func (s *Store) writeLoop() {
	for {
		first, ok := <-s.writes
		if !ok {
			return
		}
		s.publishWriteBatch(collectWriteBatch(first, s.writes))
	}
}

const maxWriteBatch = 64

func collectWriteBatch(first *writeRequest, writes <-chan *writeRequest) []*writeRequest {
	batch := []*writeRequest{first}
	timer := time.NewTimer(1 * time.Millisecond)
	defer drainTimer(timer)
	for {
		if len(batch) >= maxWriteBatch {
			return batch
		}
		select {
		case req, ok := <-writes:
			if !ok {
				return batch
			}
			batch = append(batch, req)
		case <-timer.C:
			return batch
		}
	}
}

func drainTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (s *Store) publishWriteBatch(batch []*writeRequest) {
	staged, previous, revision := s.beginWriteBatch()
	successful, staged := applyWriteRequests(batch, staged, revision)
	if len(successful) == 0 {
		return
	}
	if err := s.commitWriteBatch(previous, staged); err != nil {
		failWriteBatch(successful, err)
		return
	}
	for _, req := range successful {
		req.result <- nil
		close(req.result)
	}
}

func (s *Store) beginWriteBatch() (Definition, Definition, uint64) {
	s.mu.Lock()
	staged := cloneDefinition(s.definition)
	previous := s.definition
	revision := s.revision
	s.mu.Unlock()
	return staged, previous, revision
}

func applyWriteRequests(batch []*writeRequest, staged Definition, revision uint64) ([]*writeRequest, Definition) {
	successful := make([]*writeRequest, 0, len(batch))
	claimed := map[uint64]bool{}
	for _, req := range batch {
		if req.requireRevision && (req.expectedRevision != revision || claimed[req.expectedRevision]) {
			req.result <- ErrRevisionConflict
			close(req.result)
			continue
		}
		if req.requireRevision {
			claimed[req.expectedRevision] = true
		}
		next, err := req.apply(staged)
		if err != nil {
			req.result <- err
			close(req.result)
			continue
		}
		staged = next
		successful = append(successful, req)
	}
	return successful, staged
}

func (s *Store) commitWriteBatch(previous, staged Definition) error {
	refreshOrderedIndexCaches(previous, &staged)
	if err := validatePublishedDefinition(s, previous, staged); err != nil {
		return err
	}
	prepared, err := s.prepareRowSync(previous, staged)
	if err != nil {
		return err
	}
	if err := prepared.commit(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !sameSchema(s.definition, staged) {
		if err := s.persistLocked(staged); err != nil {
			return err
		}
	}
	s.definition = staged
	s.revision++
	return nil
}

func failWriteBatch(batch []*writeRequest, err error) {
	for _, req := range batch {
		req.result <- err
		close(req.result)
	}
}
