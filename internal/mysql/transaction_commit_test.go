package mysql

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestConcurrentAutocommitInsertsDoNotDeadlock(t *testing.T) {
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })

	setup := &textStatementExecutor{session: &session{
		server: server, database: "app", initialDB: "app",
		timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{},
	}}
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE items (id INT PRIMARY KEY, label VARCHAR(32) NOT NULL)",
	} {
		if _, err := executeStatement(setup, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	const workers = 50
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			executor := &textStatementExecutor{session: &session{
				server: server, database: "app", initialDB: "app",
				timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{},
			}}
			query := fmt.Sprintf("INSERT INTO items VALUES (%d, 'row-%d')", id+1, id+1)
			if _, err := executeStatement(executor, query); err != nil {
				failures <- err
			}
		}(worker)
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent autocommit insert: %v", err)
	}

	result, err := executeStatement(setup, "SELECT COUNT(*) FROM items")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(result.rows) != 1 || result.rows[0][0] != fmt.Sprintf("%d", workers) {
		t.Fatalf("row count = %#v, want %d", result.rows, workers)
	}
}

func TestAutocommitInsertUsesDirectDurablePublication(t *testing.T) {
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	executor := &textStatementExecutor{session: &session{
		server: server, database: "app", initialDB: "app",
		timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{},
	}}
	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE items (id INT PRIMARY KEY, label VARCHAR(32) NOT NULL)",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	if _, err := executeStatement(executor, "INSERT INTO items VALUES (1, 'ada')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if executor.session.inTransaction() {
		t.Fatal("autocommit insert left an open transaction")
	}
	result, err := executeStatement(executor, "SELECT label FROM items WHERE id = 1")
	if err != nil || len(result.rows) != 1 || result.rows[0][0] != "ada" {
		t.Fatalf("rows = %#v err=%v", result, err)
	}
}

func TestCatalogMutationFailureMapsTypedCatalogErrors(t *testing.T) {
	fallback := sqlFailure{1105, "HY000", ""}
	cases := []struct {
		name string
		err  error
		want sqlFailure
	}{
		{"revision conflict", fmt.Errorf("publish: %w", catalog.ErrRevisionConflict), sqlFailure{1213, "40001", "Deadlock found when trying to get lock; try restarting transaction"}},
		{"duplicate key", fmt.Errorf("publish: %w", catalog.ErrDuplicateKey), sqlFailure{1062, "23000", "Duplicate entry for key 'PRIMARY'"}},
		{"sql failure", sqlFailure{1452, "23000", "foreign key"}, sqlFailure{1452, "23000", "foreign key"}},
		{"other", errors.New("table has a duplicate key name"), sqlFailure{1105, "HY000", "table has a duplicate key name"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var got sqlFailure
			if !errors.As(catalogMutationFailure(test.err, fallback), &got) || got != test.want {
				t.Fatalf("failure = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestSavepointFailureMapsMissingSavepoint(t *testing.T) {
	var got sqlFailure
	if !errors.As(savepointFailure(catalog.ErrSavepointNotFound), &got) || got.code != 1305 || got.state != "42000" {
		t.Fatalf("failure = %#v, want 1305 42000", got)
	}
	other := errors.New("other")
	if err := savepointFailure(other); err != other {
		t.Fatalf("failure = %v, want the original error", err)
	}
	if err := savepointFailure(nil); err != nil {
		t.Fatalf("failure = %v, want nil", err)
	}
}
