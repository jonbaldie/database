package mysql

import (
	"encoding/json"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/instance"
)

func backupExecutor(t *testing.T, username string, grants []catalog.Grant) *textStatementExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	metadata := instance.Metadata{
		Schema: "database.instance/v1", InstanceID: "source-live", State: "stopped",
		AdminAccount: "admin", PasswordHash: "hash", DataVersion: instance.CurrentDataVersion,
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{
		Catalog: store, Username: "admin", PasswordHash: "hash", Instance: metadata, Version: "0.1.0",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	if username != "admin" {
		if err := store.CreateAccount(catalog.Account{Name: username, PasswordHash: "hash", Grants: grants}); err != nil {
			t.Fatalf("create account: %v", err)
		}
	}
	return &textStatementExecutor{session: &session{
		server: server, username: username, database: "app", initialDB: "app",
		timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{},
	}}
}

func TestBackupInstanceCapturesCommittedCatalogSnapshot(t *testing.T) {
	executor := backupExecutor(t, "admin", nil)
	if err := executor.server.config.Catalog.CreateTableWithTypes("app", "items", []string{"id"}, []string{"INT"}); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := executor.server.config.Catalog.ReplaceRows("app", "items", [][]string{{"7"}}); err != nil {
		t.Fatalf("replace rows: %v", err)
	}
	result, err := executor.execute("BACKUP INSTANCE")
	if err != nil {
		t.Fatalf("backup instance: %v", err)
	}
	if len(result.columns) != 2 || result.columns[0] != "path" || result.columns[1] != "content" {
		t.Fatalf("columns = %#v", result.columns)
	}
	files := map[string]string{}
	for _, row := range result.rows {
		files[row[0]] = row[1]
	}
	var metadata instance.Metadata
	if err := json.Unmarshal([]byte(files["instance.json"]), &metadata); err != nil {
		t.Fatalf("decode instance: %v", err)
	}
	if metadata.InstanceID != "source-live" || metadata.State != "stopped" {
		t.Fatalf("instance metadata = %#v", metadata)
	}
	var definition catalog.Definition
	if err := json.Unmarshal([]byte(files["catalog.json"]), &definition); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	table := definition.Namespaces["app"].Tables["items"]
	if !equalRows(table.Rows, [][]string{{"7"}}) {
		t.Fatalf("catalog rows = %#v", table.Rows)
	}
}

func TestBackupInstanceRequiresOperationalControl(t *testing.T) {
	executor := backupExecutor(t, "reader", []catalog.Grant{{Privilege: "OPERATIONAL_OBSERVATION"}})
	_, err := executor.execute("BACKUP INSTANCE")
	failure, ok := err.(sqlFailure)
	if !ok || failure.code != 1227 {
		t.Fatalf("expected access denied, got %#v", err)
	}
}
