package catalog

import (
	"errors"

	"github.com/jonbaldie/database/internal/storage"
)

// ErrDuplicateKey reports that a published row would repeat a primary or
// unique key in the durable row image.
var ErrDuplicateKey = storage.ErrDuplicateKey

// ErrSavepointNotFound reports a savepoint name the transaction does not hold.
var ErrSavepointNotFound = errors.New("savepoint does not exist")

// Isolation selects which committed catalog a transaction reads.
type Isolation uint8

const (
	// RepeatableRead keeps the snapshot taken at the first read.
	RepeatableRead Isolation = iota
	// ReadCommitted reads the latest commit for each statement until the
	// transaction stages a mutation.
	ReadCommitted
)

// Txn is one session's private catalog transaction. It owns the working
// snapshot, the staged mutations that replay at commit, and the savepoints
// that restore both. A nil store reads as an empty catalog and publishes
// nothing. A Txn is not safe for concurrent use and must not be used after
// Commit, CommitAtLatest, or Abort.
type Txn struct {
	store      *Store
	isolation  Isolation
	working    txnState
	savepoints []txnSavepoint
}

type txnState struct {
	snapshot  Definition
	revision  uint64
	loaded    bool
	read      bool
	dirty     bool
	mutations []func(*Definition) error
}

type txnSavepoint struct {
	name  string
	state txnState
}

// BeginTxn starts a catalog transaction. It takes no snapshot until the first
// read, stage, or savepoint.
func BeginTxn(store *Store, isolation Isolation) *Txn {
	return &Txn{store: store, isolation: isolation}
}

// Dirty reports whether the transaction holds staged mutations.
func (t *Txn) Dirty() bool {
	return t.working.dirty
}

// StatementView returns the catalog one statement reads. A write statement
// always reads the working snapshot. A read statement fixes the repeatable
// read snapshot and otherwise reads the latest commit until the transaction is
// dirty.
func (t *Txn) StatementView(forWrite bool) (Definition, error) {
	if err := t.load(); err != nil {
		return Definition{}, err
	}
	if !forWrite {
		t.working.read = true
	}
	if forWrite || t.working.dirty || t.isolation == RepeatableRead && t.working.read {
		return t.working.snapshot, nil
	}
	return t.latest(), nil
}

// Current returns the catalog the transaction reads outside a statement view.
func (t *Txn) Current() Definition {
	_ = t.load()
	if t.working.dirty || t.isolation == RepeatableRead {
		return t.working.snapshot
	}
	return t.latest()
}

// Stage applies action to a private copy of the working snapshot. The store's
// constraint validator checks the result, then admit may veto it. Only an
// admitted change becomes visible to later reads and replays at commit.
func (t *Txn) Stage(action func(*Definition) error, admit func() error) error {
	if err := t.load(); err != nil {
		return err
	}
	staged, err := Apply(t.working.snapshot, action)
	if err != nil {
		return err
	}
	if err := t.store.validateConstraints(t.working.snapshot, staged); err != nil {
		return err
	}
	if admit != nil {
		if err := admit(); err != nil {
			return err
		}
	}
	t.working.snapshot = staged
	t.working.dirty = true
	t.working.mutations = append(t.working.mutations, action)
	return nil
}

// Savepoint records the working state under name. A savepoint with an equal
// SQL identifier is replaced and moves to the newest position.
func (t *Txn) Savepoint(name string) error {
	if err := t.load(); err != nil {
		return err
	}
	if index := t.savepointIndex(name); index >= 0 {
		t.savepoints = append(t.savepoints[:index], t.savepoints[index+1:]...)
	}
	t.savepoints = append(t.savepoints, txnSavepoint{name: name, state: t.working})
	return nil
}

// RollbackTo restores the working state recorded by name and discards every
// later savepoint. The named savepoint remains.
func (t *Txn) RollbackTo(name string) error {
	index := t.savepointIndex(name)
	if index < 0 {
		return ErrSavepointNotFound
	}
	restored := t.savepoints[index].state
	restored.loaded = true
	restored.mutations = append([]func(*Definition) error(nil), restored.mutations...)
	t.working = restored
	t.savepoints = t.savepoints[:index+1]
	return nil
}

// Release forgets the savepoint recorded by name.
func (t *Txn) Release(name string) error {
	index := t.savepointIndex(name)
	if index < 0 {
		return ErrSavepointNotFound
	}
	t.savepoints = append(t.savepoints[:index], t.savepoints[index+1:]...)
	return nil
}

// Commit replays the staged mutations durably only if no other commit
// published since the working snapshot. Otherwise it reports
// ErrRevisionConflict. The transaction ends on success and on failure.
func (t *Txn) Commit() error {
	return t.publish(true)
}

// CommitAtLatest replays the staged mutations durably on the latest commit
// without a revision check. The transaction ends on success and on failure.
func (t *Txn) CommitAtLatest() error {
	return t.publish(false)
}

// Abort discards the staged mutations and savepoints.
func (t *Txn) Abort() {
	t.working = txnState{}
	t.savepoints = nil
}

func (t *Txn) publish(requireRevision bool) error {
	defer t.Abort()
	if !t.working.dirty || t.store == nil {
		return nil
	}
	mutations := t.working.mutations
	replay := func(base Definition) (Definition, error) {
		for _, mutation := range mutations {
			if err := mutation(&base); err != nil {
				return Definition{}, err
			}
		}
		return base, nil
	}
	if requireRevision {
		return t.store.ApplyDurableIfRevision(t.working.revision, replay)
	}
	return t.store.ApplyDurable(replay)
}

// load takes the working snapshot on first use. A dirty snapshot that is not
// pinned by a repeatable read is rebuilt on the latest commit so a read
// committed transaction sees other commits beneath its own staged work.
func (t *Txn) load() error {
	if t.working.loaded && !(t.working.dirty && (t.isolation == ReadCommitted || !t.working.read)) {
		return nil
	}
	definition, revision := t.latestWithRevision()
	if t.working.loaded {
		for _, mutation := range t.working.mutations {
			staged, err := Apply(definition, mutation)
			if err != nil {
				return err
			}
			definition = staged
		}
	}
	t.working.snapshot, t.working.revision, t.working.loaded = definition, revision, true
	return nil
}

func (t *Txn) latest() Definition {
	definition, _ := t.latestWithRevision()
	return definition
}

func (t *Txn) latestWithRevision() (Definition, uint64) {
	if t.store == nil {
		return Definition{Namespaces: map[string]Namespace{}}, 0
	}
	return t.store.SnapshotWithRevision()
}

func (t *Txn) savepointIndex(name string) int {
	key := Key(name)
	for index := len(t.savepoints) - 1; index >= 0; index-- {
		if Key(t.savepoints[index].name) == key {
			return index
		}
	}
	return -1
}
