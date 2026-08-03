package storage

import (
	"sync"
	"time"
)

type syncRequest struct {
	done chan error
}

func (e *Engine) ensureSyncLoop() {
	e.syncOnce.Do(func() {
		e.syncQueue = make(chan *syncRequest, 1024)
		go e.syncLoop()
	})
}

func (e *Engine) syncLoop() {
	for {
		first, ok := <-e.syncQueue
		if !ok {
			return
		}
		batch := []*syncRequest{first}
		timer := time.NewTimer(500 * time.Microsecond)
	collect:
		for len(batch) < 64 {
			select {
			case req, ok := <-e.syncQueue:
				if !ok {
					break collect
				}
				batch = append(batch, req)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		e.finishSyncBatch(batch)
	}
}

func (e *Engine) finishSyncBatch(batch []*syncRequest) {
	err := e.syncWAL()
	for _, req := range batch {
		req.done <- err
		close(req.done)
	}
}

func (e *Engine) syncWAL() error {
	e.mu.Lock()
	file := e.wal
	e.mu.Unlock()
	if file == nil {
		return errClosed
	}
	return file.Sync()
}

func (e *Engine) awaitGroupSync() error {
	e.mu.Lock()
	file := e.wal
	e.mu.Unlock()
	if file == nil {
		return errClosed
	}
	return file.Sync()
}

var _ = sync.Mutex{}
