package catalog

import (
	"errors"
	"reflect"
	"testing"
)

func openTxnStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = store.ApplyDurable(func(definition Definition) (Definition, error) {
		definition.Namespaces["app"] = Namespace{Name: "app", Tables: map[string]Table{"items": {
			Name:        "items",
			Columns:     []string{"id"},
			Constraints: []Constraint{{Name: "PRIMARY", Type: ConstraintTypePrimary, Columns: []string{"id"}}},
			Rows:        [][]string{{"1"}},
		}}}
		return definition, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func insertItem(id string) func(*Definition) error {
	return func(definition *Definition) error {
		namespace := definition.Namespaces["app"]
		table := namespace.Tables["items"]
		table.Rows = append(table.Rows, []string{id})
		namespace.Tables["items"] = table
		definition.Namespaces["app"] = namespace
		return nil
	}
}

func itemIDs(definition Definition) []string {
	var ids []string
	for _, row := range definition.Namespaces["app"].Tables["items"].Rows {
		ids = append(ids, row[0])
	}
	return ids
}

func requireItems(t *testing.T, label string, definition Definition, want ...string) {
	t.Helper()
	if got := itemIDs(definition); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s items = %v, want %v", label, got, want)
	}
}

func publishItem(t *testing.T, store *Store, id string) {
	t.Helper()
	err := store.ApplyDurable(func(definition Definition) (Definition, error) {
		return definition, insertItem(id)(&definition)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readView(t *testing.T, txn *Txn) Definition {
	t.Helper()
	view, err := txn.StatementView(false)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestTxnRepeatableReadKeepsItsFirstReadSnapshot(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	requireItems(t, "first read", readView(t, txn), "1")

	publishItem(t, store, "2")

	requireItems(t, "second read", readView(t, txn), "1")
	requireItems(t, "current", txn.Current(), "1")
}

func TestTxnReadCommittedSeesEachLatestCommit(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, ReadCommitted)
	requireItems(t, "first read", readView(t, txn), "1")

	publishItem(t, store, "2")

	requireItems(t, "second read", readView(t, txn), "1", "2")
	requireItems(t, "current", txn.Current(), "1", "2")
}

func TestTxnStagedMutationsStayPrivateUntilCommit(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	if txn.Dirty() {
		t.Fatal("new transaction is dirty")
	}
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	if !txn.Dirty() {
		t.Fatal("staged transaction is not dirty")
	}
	requireItems(t, "transaction", txn.Current(), "1", "2")
	requireItems(t, "store before commit", store.Snapshot(), "1")

	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	requireItems(t, "store after commit", store.Snapshot(), "1", "2")
	if txn.Dirty() {
		t.Fatal("committed transaction is still dirty")
	}
}

func TestTxnReadCommittedReplaysStagedMutationsOnTheLatestCommit(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, ReadCommitted)
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	publishItem(t, store, "3")

	requireItems(t, "replayed read", readView(t, txn), "1", "3", "2")
	if err := txn.Commit(); err != nil {
		t.Fatalf("commit after replay: %v", err)
	}
	requireItems(t, "store", store.Snapshot(), "1", "3", "2")
}

func TestTxnCommitReportsRevisionConflict(t *testing.T) {
	store := openTxnStore(t)
	first := BeginTxn(store, RepeatableRead)
	second := BeginTxn(store, RepeatableRead)
	requireItems(t, "first read", readView(t, first), "1")
	requireItems(t, "second read", readView(t, second), "1")
	if err := first.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	if err := second.Stage(insertItem("3"), nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}

	err := second.Commit()
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("second commit error = %v, want ErrRevisionConflict", err)
	}
	requireItems(t, "store", store.Snapshot(), "1", "2")
	if second.Dirty() {
		t.Fatal("failed commit left staged mutations")
	}
}

func TestTxnCommitAtLatestReplaysWithoutRevisionCheck(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	publishItem(t, store, "3")

	if err := txn.CommitAtLatest(); err != nil {
		t.Fatal(err)
	}
	requireItems(t, "store", store.Snapshot(), "1", "3", "2")
}

func TestTxnCommitReportsTypedDuplicateKey(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	if err := txn.Stage(insertItem("1"), nil); err != nil {
		t.Fatal(err)
	}

	err := txn.Commit()
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("commit error = %v, want ErrDuplicateKey", err)
	}
	requireItems(t, "store", store.Snapshot(), "1")
}

func TestTxnStageUsesTheStoreConstraintValidator(t *testing.T) {
	store := openTxnStore(t)
	rejected := errors.New("constraint rejected")
	var calls int
	store.SetPublishValidator(func(previous, next Definition) error {
		calls++
		if len(itemIDs(next)) > 2 {
			return rejected
		}
		return nil
	})
	txn := BeginTxn(store, RepeatableRead)
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	if err := txn.Stage(insertItem("3"), nil); !errors.Is(err, rejected) {
		t.Fatalf("stage error = %v, want validator error", err)
	}
	requireItems(t, "after rejected stage", txn.Current(), "1", "2")
	if calls != 2 {
		t.Fatalf("validator calls = %d, want 2", calls)
	}
}

func TestTxnStageAdmitCanVetoAValidatedChange(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	vetoed := errors.New("statement cancelled")
	if err := txn.Stage(insertItem("2"), func() error { return vetoed }); !errors.Is(err, vetoed) {
		t.Fatalf("stage error = %v, want admit error", err)
	}
	if txn.Dirty() {
		t.Fatal("vetoed stage made the transaction dirty")
	}
	requireItems(t, "after veto", txn.Current(), "1")
}

func TestTxnStageReportsActionError(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	failed := errors.New("bad mutation")
	if err := txn.Stage(func(*Definition) error { return failed }, nil); !errors.Is(err, failed) {
		t.Fatalf("stage error = %v, want action error", err)
	}
	if txn.Dirty() {
		t.Fatal("failed stage made the transaction dirty")
	}
}

func TestTxnSavepointsRestoreStagedState(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	mustSavepoint(t, txn, "clean")
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	mustSavepoint(t, txn, "Two")
	if err := txn.Stage(insertItem("3"), nil); err != nil {
		t.Fatal(err)
	}
	mustSavepoint(t, txn, "three")

	if err := txn.RollbackTo("two"); err != nil {
		t.Fatal(err)
	}
	requireItems(t, "after rollback to two", txn.Current(), "1", "2")
	if err := txn.RollbackTo("three"); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("later savepoint error = %v, want ErrSavepointNotFound", err)
	}
	if err := txn.RollbackTo("TWO"); err != nil {
		t.Fatalf("savepoint must survive its own rollback: %v", err)
	}

	if err := txn.RollbackTo("clean"); err != nil {
		t.Fatal(err)
	}
	if txn.Dirty() {
		t.Fatal("rollback to a clean savepoint left the transaction dirty")
	}
	requireItems(t, "after rollback to clean", txn.Current(), "1")
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	requireItems(t, "store", store.Snapshot(), "1")
}

func TestTxnRollbackToSavepointDropsLaterMutationsFromCommit(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	mustSavepoint(t, txn, "kept")
	if err := txn.Stage(insertItem("3"), nil); err != nil {
		t.Fatal(err)
	}
	if err := txn.RollbackTo("kept"); err != nil {
		t.Fatal(err)
	}
	if err := txn.CommitAtLatest(); err != nil {
		t.Fatal(err)
	}
	requireItems(t, "store", store.Snapshot(), "1", "2")
}

func TestTxnSavepointReuseAndRelease(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	mustSavepoint(t, txn, "first")
	mustSavepoint(t, txn, "marker")
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	mustSavepoint(t, txn, "MARKER")
	if err := txn.RollbackTo("first"); err != nil {
		t.Fatal(err)
	}
	if err := txn.Release("marker"); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("release error = %v, want ErrSavepointNotFound after rollback", err)
	}
	if err := txn.Release("first"); err != nil {
		t.Fatal(err)
	}
	if err := txn.RollbackTo("first"); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("rollback error = %v, want ErrSavepointNotFound after release", err)
	}
}

func TestTxnSavepointRedefinitionMovesTheMarker(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	mustSavepoint(t, txn, "marker")
	mustSavepoint(t, txn, "after")
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	mustSavepoint(t, txn, "Marker")
	if err := txn.Stage(insertItem("3"), nil); err != nil {
		t.Fatal(err)
	}
	if err := txn.RollbackTo("marker"); err != nil {
		t.Fatal(err)
	}
	requireItems(t, "after rollback", txn.Current(), "1", "2")
	if err := txn.RollbackTo("after"); err != nil {
		t.Fatalf("earlier savepoint must survive a later redefinition: %v", err)
	}
	requireItems(t, "after rollback to earlier", txn.Current(), "1")
}

func TestTxnAbortDiscardsStagedMutations(t *testing.T) {
	store := openTxnStore(t)
	txn := BeginTxn(store, RepeatableRead)
	if err := txn.Stage(insertItem("2"), nil); err != nil {
		t.Fatal(err)
	}
	mustSavepoint(t, txn, "marker")
	txn.Abort()
	if txn.Dirty() {
		t.Fatal("aborted transaction is dirty")
	}
	if err := txn.RollbackTo("marker"); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("rollback error = %v, want ErrSavepointNotFound after abort", err)
	}
	requireItems(t, "store", store.Snapshot(), "1")
}

func TestTxnWithoutStoreReadsAnEmptyCatalog(t *testing.T) {
	txn := BeginTxn(nil, ReadCommitted)
	view := readView(t, txn)
	if view.Namespaces == nil || len(view.Namespaces) != 0 {
		t.Fatalf("view = %#v, want an empty catalog", view)
	}
	if current := txn.Current(); current.Namespaces == nil || len(current.Namespaces) != 0 {
		t.Fatalf("current = %#v, want an empty catalog", current)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mustSavepoint(t *testing.T, txn *Txn, name string) {
	t.Helper()
	if err := txn.Savepoint(name); err != nil {
		t.Fatal(err)
	}
}
