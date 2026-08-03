package catalog

import "time"

type writeRequest struct {
	apply  func(Definition) (Definition, error)
	result chan error
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
	s.ensureWriter()
	req := &writeRequest{apply: apply, result: make(chan error, 1)}
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
	s.mu.Unlock()
	successful := make([]*writeRequest, 0, len(batch))
	for _, req := range batch {
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
	warmPrimaryIndexesFrom(previous, staged)
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

func warmPrimaryIndexesFrom(previous, next Definition) {
	for namespaceKey, namespace := range next.Namespaces {
		previousNamespace := previous.Namespaces[namespaceKey]
		for tableKey, table := range namespace.Tables {
			if table.PrimaryIndex != nil {
				continue
			}
			previousTable := previousNamespace.Tables[tableKey]
			if appendOnlyRowImage(previousTable.Rows, table.Rows) {
				// Append-only publishes keep the catalog PK index cold; point
				// lookups and duplicate checks use the durable row engine.
				continue
			}
			if extendPrimaryIndex(&table, previousTable) {
				namespace.Tables[tableKey] = table
				continue
			}
			RebuildPrimaryIndex(&table)
			namespace.Tables[tableKey] = table
		}
		next.Namespaces[namespaceKey] = namespace
	}
}

func extendPrimaryIndex(table *Table, previous Table) bool {
	if previous.PrimaryIndex == nil || !appendOnlyRowImage(previous.Rows, table.Rows) {
		return false
	}
	primary, _ := tableKeyColumns(*table)
	if len(primary) == 0 {
		return false
	}
	nextIndex := make(map[string]int, len(previous.PrimaryIndex)+len(table.Rows)-len(previous.Rows))
	for key, value := range previous.PrimaryIndex {
		nextIndex[key] = value
	}
	indexes := columnPositions(*table, primary)
	for rowIndex := len(previous.Rows); rowIndex < len(table.Rows); rowIndex++ {
		nextIndex[rowKey(table.Rows[rowIndex], indexes)] = rowIndex
	}
	table.PrimaryIndex = nextIndex
	return true
}

func failWriteBatch(batch []*writeRequest, err error) {
	for _, req := range batch {
		req.result <- err
		close(req.result)
	}
}
