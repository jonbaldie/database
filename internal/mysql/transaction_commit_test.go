package mysql

import (
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
		if _, err := setup.execute(query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}

	const workers = 50
	errors := make(chan error, workers)
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
			if _, err := executor.execute(query); err != nil {
				errors <- err
			}
		}(worker)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent autocommit insert: %v", err)
	}

	result, err := setup.execute("SELECT COUNT(*) FROM items")
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
		if _, err := executor.execute(query); err != nil {
			t.Fatalf("setup %q: %v", query, err)
		}
	}
	if _, err := executor.execute("INSERT INTO items VALUES (1, 'ada')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if executor.session.transaction {
		t.Fatal("autocommit insert left an open transaction")
	}
	result, err := executor.execute("SELECT label FROM items WHERE id = 1")
	if err != nil || len(result.rows) != 1 || result.rows[0][0] != "ada" {
		t.Fatalf("rows = %#v err=%v", result, err)
	}
}
