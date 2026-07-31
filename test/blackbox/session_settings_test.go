package blackbox_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLSessionSettingsUseTheWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := t.TempDir()
	initializeServer(t, runner, directory, "settings-secret")
	mysqlAddress := freeAddress(t)
	process, err := runner.Start(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", mysqlAddress, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	nextReadyEvent(t, process)
	client := newWireClient(t, mysqlAddress, "admin", "settings-secret")
	defer client.close()

	for _, query := range []string{
		"SET SESSION transaction_isolation = 'READ-COMMITTED'",
		"SET SESSION transaction_read_only = ON",
		"SET time_zone = '+05:30'",
		"SET NAMES utf8mb4 COLLATE utf8mb4_bin",
		"SET statement_timeout_ms = 1000, lock_wait_timeout_ms = 1000",
		"SET execution_memory_limit_bytes = 1024, temporary_storage_limit_bytes = 1024",
	} {
		mustQuery(t, client, query)
	}
	for query, want := range map[string]string{
		"SELECT @@transaction_isolation":         "READ-COMMITTED",
		"SELECT @@transaction_read_only":         "ON",
		"SELECT @@time_zone":                     "+05:30",
		"SELECT @@collation_connection":          "utf8mb4_bin",
		"SELECT @@statement_timeout_ms":          "1000",
		"SELECT @@execution_memory_limit_bytes":  "1024",
		"SELECT @@temporary_storage_limit_bytes": "1024",
		"SELECT @@version":                       "8.4.11-database-0.1.0-dev",
		"SELECT VERSION()":                       "8.4.11-database-0.1.0-dev",
	} {
		result := client.query(query)
		if result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{want}}) {
			t.Fatalf("%s = %#v, want %q", query, result, want)
		}
	}
	if result := client.query("SHOW SESSION VARIABLES LIKE 'time_zone'"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"time_zone", "+05:30"}}) {
		t.Fatalf("show session variable: %#v", result)
	}
	for _, query := range []string{
		"SET execution_memory_limit_bytes = 67108865",
		"SET foreign_key_checks = 0",
		"SET GLOBAL max_connections = 1",
		"SET character_set_client = latin1",
	} {
		if result := client.query(query); result.err == "" {
			t.Fatalf("weak or unsupported setting succeeded: %s", query)
		}
	}
	if result := client.query("CREATE DATABASE read_only_reject"); result.err == "" {
		t.Fatalf("read-only session accepted a schema change: %#v", result)
	}
	mustQuery(t, client, "SET transaction_read_only = OFF")
	mustQuery(t, client, "SET time_zone = DEFAULT")
	if result := client.query("SELECT @@time_zone"); result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{"+00:00"}}) {
		t.Fatalf("time zone default: %#v", result)
	}
	if err := client.reset(); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]string{
		"SELECT @@transaction_isolation":        "REPEATABLE-READ",
		"SELECT @@transaction_read_only":        "OFF",
		"SELECT @@collation_connection":         "utf8mb4_0900_ai_ci",
		"SELECT @@statement_timeout_ms":         "300000",
		"SELECT @@execution_memory_limit_bytes": "67108864",
	} {
		result := client.query(query)
		if result.err != "" || !reflect.DeepEqual(result.rows, [][]string{{want}}) {
			t.Fatalf("reset %s = %#v, want %q", query, result, want)
		}
	}
}
