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
		batch := []*writeRequest{first}
		timer := time.NewTimer(200 * time.Microsecond)
	collect:
		for len(batch) < 64 {
			select {
			case req, ok := <-s.writes:
				if !ok {
					timer.Stop()
					s.publishWriteBatch(batch)
					return
				}
				batch = append(batch, req)
			case <-timer.C:
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		s.publishWriteBatch(batch)
	}
}

func (s *Store) publishWriteBatch(batch []*writeRequest) {
	s.mu.Lock()
	staged := cloneDefinition(s.definition)
	previous := s.definition
	revision := s.revision
	s.mu.Unlock()
	detachPrimaryIndexes(staged)
	successful := make([]*writeRequest, 0, len(batch))
	claimedRevisions := map[uint64]bool{}
	for _, req := range batch {
		if req.requireRevision {
			if req.expectedRevision != revision || claimedRevisions[req.expectedRevision] {
				req.result <- ErrRevisionConflict
				close(req.result)
				continue
			}
			claimedRevisions[req.expectedRevision] = true
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
	if len(successful) == 0 {
		return
	}
	if err := validatePublishedDefinition(s, previous, staged); err != nil {
		failWriteBatch(successful, err)
		return
	}
	prepared, err := s.prepareRowSync(previous, staged)
	if err != nil {
		failWriteBatch(successful, err)
		return
	}
	if err := prepared.commit(); err != nil {
		failWriteBatch(successful, err)
		return
	}
	s.mu.Lock()
	schemaChanged := !sameSchema(s.definition, staged)
	if schemaChanged {
		if err := s.persistLocked(staged); err != nil {
			s.mu.Unlock()
			failWriteBatch(successful, err)
			return
		}
	}
	s.definition = staged
	s.revision++
	s.mu.Unlock()
	for _, req := range successful {
		req.result <- nil
		close(req.result)
	}
}

func failWriteBatch(batch []*writeRequest, err error) {
	for _, req := range batch {
		req.result <- err
		close(req.result)
	}
}
