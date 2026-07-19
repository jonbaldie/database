package blackbox_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jonbaldie/database/test/blackbox"
)

var executable string

func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	directory, err := os.MkdirTemp("", "database-blackbox-")
	if err != nil {
		os.Exit(1)
	}
	executable = filepath.Join(directory, "database")
	build := exec.Command("go", "build", "-trimpath", "-o", executable, "./cmd/database")
	build.Dir = root
	if err := build.Run(); err != nil {
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}

func TestExecutableVersionAndLifecycleArePublic(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	human := runner.Run(ctx, "version")
	if human.ExitCode != 0 || human.Stdout == "" || human.Stderr != "" {
		t.Fatalf("version command: %#v", human)
	}
	var version map[string]any
	machine := runner.Run(ctx, "version", "--format=json")
	if machine.ExitCode != 0 || json.Unmarshal([]byte(machine.Stdout), &version) != nil {
		t.Fatalf("machine version: %#v", machine)
	}
	if version["schema"] != "database.version/v1" || version["product_version"] == "" {
		t.Fatalf("incomplete version contract: %#v", version)
	}

	directory := initializedInstance(t, runner)
	process, address := startServer(t, runner, directory)
	var live map[string]string
	status, err := blackbox.HTTPJSON(ctx, address, "/live", &live)
	if err != nil || status != 200 || live["status"] != "live" {
		t.Fatalf("live probe: status=%d body=%v err=%v", status, live, err)
	}
	var ready map[string]string
	status, err = blackbox.HTTPJSON(ctx, address, "/ready", &ready)
	if err != nil || status != 200 || ready["status"] != "ready" {
		t.Fatalf("ready probe: status=%d body=%v err=%v", status, ready, err)
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("graceful stop: %#v", result)
	}
}

func TestInitializeCreatesStoppedInspectableInstance(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "initial-password")
	password := "correct horse battery staple\n"
	if err := os.WriteFile(passwordFile, []byte(password), 0o600); err != nil {
		t.Fatal(err)
	}

	result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json")
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("init result: %#v", result)
	}
	var output struct {
		Schema        string `json:"schema"`
		Operation     string `json:"operation"`
		Success       bool   `json:"success"`
		ExitClass     string `json:"exit_class"`
		InstanceID    string `json:"instance_id"`
		DataDirectory string `json:"data_directory"`
		AdminAccount  string `json:"admin_account"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &output); err != nil {
		t.Fatalf("decode init result %q: %v", result.Stdout, err)
	}
	if output.Schema != "database.operator.result/v1" || output.Operation != "init" || !output.Success || output.ExitClass != "success" {
		t.Fatalf("init output = %#v", output)
	}
	if output.InstanceID == "" || output.DataDirectory != directory || output.AdminAccount != "admin" {
		t.Fatalf("init identity = %#v", output)
	}
	metadata, err := os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), strings.TrimSpace(password)) || strings.Contains(result.Stdout, strings.TrimSpace(password)) || strings.Contains(result.Stderr, strings.TrimSpace(password)) {
		t.Fatal("initial password was exposed")
	}
	var instance struct {
		Schema       string `json:"schema"`
		InstanceID   string `json:"instance_id"`
		State        string `json:"state"`
		AdminAccount string `json:"admin_account"`
		PasswordHash string `json:"password_hash"`
	}
	if err := json.Unmarshal(metadata, &instance); err != nil {
		t.Fatalf("decode instance metadata %q: %v", metadata, err)
	}
	if instance.Schema != "database.instance/v1" || instance.InstanceID != output.InstanceID || instance.State != "stopped" || instance.AdminAccount != "admin" || instance.PasswordHash == "" {
		t.Fatalf("instance metadata = %#v", instance)
	}

	second := runner.Run(context.Background(), "init", directory, "--password-stdin", "--format=json")
	if second.ExitCode != 3 || second.Stderr != "" || strings.Contains(second.Stdout+second.Stderr, strings.TrimSpace(password)) {
		t.Fatalf("unsafe reinitialization = %#v", second)
	}
	var failure struct {
		Success   bool   `json:"success"`
		ExitClass string `json:"exit_class"`
	}
	if err := json.Unmarshal([]byte(second.Stdout), &failure); err != nil || failure.Success || failure.ExitClass != "precondition" {
		t.Fatalf("precondition result = %q", second.Stdout)
	}
}

func TestInitializeAcceptsStdinAndRejectsInlinePassword(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "stdin-instance")
	result := runner.RunWithStdin(context.Background(), "stdin-secret\n", "init", directory, "--password-stdin", "--format=json")
	if result.ExitCode != 0 || result.Stderr != "" || strings.Contains(result.Stdout, "stdin-secret") {
		t.Fatalf("stdin init result: %#v", result)
	}

	invalid := runner.Run(context.Background(), "init", filepath.Join(t.TempDir(), "invalid"), "--password=inline-secret", "--format=json")
	if invalid.ExitCode != 2 || invalid.Stderr != "" || strings.Contains(invalid.Stdout+invalid.Stderr, "inline-secret") {
		t.Fatalf("inline password result: %#v", invalid)
	}
}

func TestInitializeRejectsAmbiguousOrMalformedSecretInputs(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	secret := "must-not-be-consumed"
	for _, args := range [][]string{
		{"init", filepath.Join(t.TempDir(), "empty-file"), "--password-file=", "--format=json"},
		{"init", filepath.Join(t.TempDir(), "valued-stdin"), "--password-stdin=unexpected", "--format=json"},
		{"init", filepath.Join(t.TempDir(), "unknown-flag"), "--unknown=unexpected", "--password-stdin", "--format=json"},
		{"init", "-password=" + secret, "--password-stdin", "--format=json"},
		{"init", filepath.Join(t.TempDir(), "repeated-source"), "--password-stdin", "--password-stdin", "--format=json"},
	} {
		result := runner.RunWithStdin(context.Background(), secret+"\n", args...)
		if result.ExitCode != 2 || result.Stderr != "" || strings.Contains(result.Stdout+result.Stderr, secret) {
			t.Fatalf("invalid init arguments %q: %#v", args, result)
		}
		var failure struct {
			Success   bool   `json:"success"`
			ExitClass string `json:"exit_class"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &failure); err != nil || failure.Success || failure.ExitClass != "invalid_input" {
			t.Fatalf("invalid input result %q: %v", result.Stdout, err)
		}
	}
}

func initializedInstance(t *testing.T, runner blackbox.Runner) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "instance")
	password := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(password, []byte("lifecycle-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", password, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize instance: %#v", result)
	}
	return directory
}

func startServer(t *testing.T, runner blackbox.Runner, directory string) (*blackbox.Process, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	process, err := runner.Start(context.Background(), "serve", "--format=json", "--data-directory="+directory, "--mysql-listen-address="+freeAddress(t), "--diagnostics-listen-address="+address)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var event struct {
		State     string `json:"state"`
		Address   string `json:"diagnostics_address"`
		Recovered bool   `json:"recovered"`
	}
	if err := process.NextJSONEvent(ctx, &event); err != nil {
		process.Crash()
		result := process.Wait()
		t.Fatalf("wait for ready event: %v; result=%#v", err, result)
	}
	if event.State != "ready" || event.Address != address {
		t.Fatalf("ready event: %#v", event)
	}
	return process, address
}

func TestMySQLProbeRecognizesClassicHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		payload := []byte{0x0a}
		payload = append(payload, []byte("database-test\x00")...)
		payload = append(payload, 1, 0, 0, 0)
		packet := []byte{byte(len(payload)), byte(len(payload) >> 8), 0, 0}
		_, _ = connection.Write(append(packet, payload...))
	}()
	handshake, err := blackbox.ProbeMySQL(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if handshake.ProtocolVersion != 0x0a || handshake.ServerVersion != "database-test" || handshake.ConnectionID != 1 {
		t.Fatalf("handshake = %#v", handshake)
	}
}

func TestCommandFailureIsObservable(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	result := runner.Run(context.Background(), "not-a-command")
	if result.ExitCode != 2 || result.Stderr == "" || result.Err == nil {
		t.Fatalf("failure result = %#v", result)
	}
	if errors.Is(result.Err, context.Canceled) {
		t.Fatal("unexpected cancellation")
	}
}

func TestMySQLClientCanAuthenticatePersistAndResetSession(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("secret-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	initialized := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json")
	if initialized.ExitCode != 0 {
		t.Fatalf("initialize: %#v", initialized)
	}

	diagnostics := freeAddress(t)
	mysql := freeAddress(t)
	process, err := runner.Start(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", mysql, "--diagnostics-listen-address", diagnostics, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	var event map[string]any
	if err := process.NextJSONEvent(context.Background(), &event); err != nil || event["state"] != "ready" {
		t.Fatalf("ready event: %#v %v", event, err)
	}

	client := newWireClient(t, mysql, "admin", "secret-password")
	defer client.close()
	if got := client.query("BEGIN"); got.err != "" {
		t.Fatalf("begin: %#v", got)
	}
	if got := client.query("CREATE DATABASE rolled_back"); got.err != "" {
		t.Fatalf("transactional create: %#v", got)
	}
	if got := client.query("ROLLBACK"); got.err != "" {
		t.Fatalf("rollback: %#v", got)
	}
	if got := client.query("USE rolled_back"); got.err != "" {
		t.Fatalf("DDL was not implicitly committed: %#v", got)
	}
	if got := client.query("CREATE DATABASE app"); got.err != "" {
		t.Fatalf("create database: %#v", got)
	}
	if got := client.query("USE app"); got.err != "" {
		t.Fatalf("use database: %#v", got)
	}
	if got := client.query("CREATE TABLE users (id INT, name VARCHAR(32))"); got.err != "" {
		t.Fatalf("create table: %#v", got)
	}
	if got := client.query("INSERT INTO users VALUES (1, 'Ada')"); got.err != "" {
		t.Fatalf("insert row: %#v", got)
	}
	rows := client.query("SELECT * FROM users")
	if rows.err != "" || len(rows.rows) != 1 || strings.Join(rows.rows[0], ",") != "1,Ada" || strings.Join(rows.columns, ",") != "id,name" {
		t.Fatalf("select result: %#v", rows)
	}
	prepared := client.prepare("SELECT 1")
	if prepared.err != "" {
		t.Fatalf("prepare: %#v", prepared)
	}
	preparedResult := client.executePrepared(prepared.id)
	if preparedResult.err != "" || len(preparedResult.rows) != 1 || preparedResult.rows[0][0] != "1" {
		t.Fatalf("prepared result: %#v", preparedResult)
	}
	client.closePrepared(prepared.id)
	if err := client.reset(); err != nil {
		t.Fatal(err)
	}
	if got := client.query("SELECT DATABASE()"); got.err != "" || got.rows[0][0] != "" {
		t.Fatalf("reset did not restore initial namespace: %#v", got)
	}

	if err := client.close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("stop: %#v", result)
	}
	process = nil
	process, err = runner.Start(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", mysql, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	var restarted map[string]any
	if err := process.NextJSONEvent(context.Background(), &restarted); err != nil || restarted["state"] != "ready" {
		t.Fatalf("restart: %#v %v", restarted, err)
	}
	reopened := newWireClient(t, mysql, "admin", "secret-password")
	defer reopened.close()
	rows = reopened.query("USE app")
	if rows.err != "" {
		t.Fatalf("reopen database: %#v", rows)
	}
	rows = reopened.query("SELECT * FROM users")
	if rows.err != "" || len(rows.rows) != 1 || strings.Join(rows.rows[0], ",") != "1,Ada" {
		t.Fatalf("durable row: %#v", rows)
	}
}

func TestMySQLTransactionsProvideIsolationAndReadYourOwnWrites(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("transaction-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	first := newWireClient(t, address, "admin", "transaction-secret")
	defer first.close()
	second := newWireClient(t, address, "admin", "transaction-secret")
	defer second.close()

	for _, query := range []string{
		"CREATE DATABASE transactions",
		"USE transactions",
		"CREATE TABLE entries (id INT)",
	} {
		if result := first.query(query); result.err != "" {
			t.Fatalf("setup %s: %#v", query, result)
		}
	}
	if result := second.query("USE transactions"); result.err != "" {
		t.Fatalf("second USE: %#v", result)
	}

	if result := first.query("BEGIN"); result.err != "" {
		t.Fatalf("begin repeatable read: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("initial repeatable-read snapshot: %#v", result)
	}
	if result := second.query("CREATE TABLE metadata_visible (id INT)"); result.err != "" {
		t.Fatalf("committed concurrent DDL: %#v", result)
	}
	metadata := first.query("SELECT TABLE_NAME FROM information_schema.tables")
	foundMetadataTable := false
	for _, row := range metadata.rows {
		if len(row) > 0 && row[0] == "metadata_visible" {
			foundMetadataTable = true
		}
	}
	if metadata.err != "" || !foundMetadataTable {
		t.Fatalf("catalog metadata was transaction-frozen: %#v", metadata)
	}
	if result := second.query("INSERT INTO entries VALUES (2)"); result.err != "" {
		t.Fatalf("committed concurrent insert: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("repeatable-read snapshot changed: %#v", result)
	}
	if result := first.query("INSERT INTO entries VALUES (1)"); result.err != "" {
		t.Fatalf("own insert: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("read-your-own-write: %#v", result)
	}
	if result := second.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "2" {
		t.Fatalf("uncommitted row leaked: %#v", result)
	}
	if result := first.query("ROLLBACK"); result.err != "" {
		t.Fatalf("rollback: %#v", result)
	}
	if result := second.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "2" {
		t.Fatalf("rolled-back row persisted: %#v", result)
	}

	if result := first.query("SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED"); result.err != "" {
		t.Fatalf("set read committed: %#v", result)
	}
	if result := first.query("BEGIN"); result.err != "" {
		t.Fatalf("begin read committed: %#v", result)
	}
	if result := second.query("INSERT INTO entries VALUES (3)"); result.err != "" {
		t.Fatalf("second committed insert: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 2 {
		t.Fatalf("read-committed snapshot: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 2 {
		t.Fatalf("read-committed statement snapshot: %#v", result)
	}
	if result := second.query("INSERT INTO entries VALUES (4)"); result.err != "" {
		t.Fatalf("second read-committed insert: %#v", result)
	}
	if result := first.query("INSERT INTO entries VALUES (5)"); result.err != "" {
		t.Fatalf("read-committed own insert: %#v", result)
	}
	if result := second.query("INSERT INTO entries VALUES (6)"); result.err != "" {
		t.Fatalf("third committed insert: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 5 {
		t.Fatalf("read-committed own and concurrent writes: %#v", result)
	}
	if result := first.query("COMMIT"); result.err != "" {
		t.Fatalf("read-committed commit: %#v", result)
	}
	if result := first.query("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ"); result.err != "" {
		t.Fatalf("restore repeatable read: %#v", result)
	}
	if result := first.query("BEGIN"); result.err != "" {
		t.Fatalf("begin write-before-read transaction: %#v", result)
	}
	if result := first.query("INSERT INTO entries VALUES (7)"); result.err != "" {
		t.Fatalf("repeatable-read own insert: %#v", result)
	}
	if result := second.query("INSERT INTO entries VALUES (8)"); result.err != "" {
		t.Fatalf("repeatable-read concurrent insert: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 7 {
		t.Fatalf("repeatable-read first snapshot after write: %#v", result)
	}
	if result := first.query("ROLLBACK"); result.err != "" {
		t.Fatalf("repeatable-read write rollback: %#v", result)
	}
}

func TestMySQLTransactionsEnforceAutocommitReadOnlyAndAtomicErrors(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("transaction-settings-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	first := newWireClient(t, address, "admin", "transaction-settings-secret")
	defer first.close()
	second := newWireClient(t, address, "admin", "transaction-settings-secret")
	defer second.close()
	for _, query := range []string{
		"CREATE DATABASE transaction_settings",
		"USE transaction_settings",
		"CREATE TABLE entries (id INT)",
	} {
		if result := first.query(query); result.err != "" {
			t.Fatalf("setup %s: %#v", query, result)
		}
	}
	if result := second.query("USE transaction_settings"); result.err != "" {
		t.Fatalf("second USE: %#v", result)
	}

	for _, query := range []string{
		"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE",
		"SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED",
		"SET unsupported_setting = 1",
		"RESET unsupported_setting",
	} {
		if result := first.query(query); result.err == "" {
			t.Fatalf("unsupported isolation accepted: %s", query)
		}
	}
	if result := first.query("START TRANSACTION READ ONLY"); result.err != "" {
		t.Fatalf("read-only transaction: %#v", result)
	}
	if result := first.query("INSERT INTO entries VALUES (9)"); result.err == "" {
		t.Fatal("mutation succeeded in read-only transaction")
	}
	if result := first.query("ROLLBACK"); result.err != "" {
		t.Fatalf("read-only rollback: %#v", result)
	}

	if result := first.query("SET autocommit = 0"); result.err != "" {
		t.Fatalf("disable autocommit: %#v", result)
	}
	if result := first.query("INSERT INTO entries VALUES (4)"); result.err != "" {
		t.Fatalf("implicit transaction insert: %#v", result)
	}
	if result := second.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("autocommit-off row leaked: %#v", result)
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "4" {
		t.Fatalf("autocommit-off own row: %#v", result)
	}
	if result := first.query("ROLLBACK"); result.err != "" {
		t.Fatalf("autocommit-off rollback: %#v", result)
	}
	if result := second.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("autocommit-off rollback persisted row: %#v", result)
	}
	if result := first.query("SET autocommit = 1"); result.err != "" {
		t.Fatalf("enable autocommit: %#v", result)
	}

	if result := first.query("BEGIN"); result.err != "" {
		t.Fatalf("begin atomic-error transaction: %#v", result)
	}
	if result := first.query("INSERT INTO entries VALUES (5), ('not-an-int')"); result.err == "" {
		t.Fatal("invalid multi-row mutation succeeded")
	}
	if result := first.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("failed statement left partial rows: %#v", result)
	}
	if result := first.query("COMMIT"); result.err != "" {
		t.Fatalf("atomic-error commit: %#v", result)
	}
	if result := second.query("SELECT * FROM entries"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("failed statement became durable: %#v", result)
	}
}

func TestMySQLNamespacesAndBasicTablesSurviveRestartAndSupportQualifiedAccess(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("namespace-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, address := startMySQLServer(t, runner, directory)
	client := newWireClient(t, address, "admin", "namespace-secret")
	for _, query := range []string{
		"CREATE DATABASE alpha",
		"CREATE DATABASE beta",
		"USE alpha",
		"CREATE TABLE local_rows (id INT, name VARCHAR(32))",
		"INSERT INTO local_rows VALUES (1, 'Ada')",
		"CREATE TABLE beta.cross_rows (id INT, name VARCHAR(32))",
		"INSERT INTO beta.cross_rows VALUES (2, 'Grace')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}
	if result := client.query("SELECT * FROM local_rows"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "1,Ada" {
		t.Fatalf("current namespace read: %#v", result)
	}
	if result := client.query("SELECT * FROM beta.cross_rows"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "2,Grace" {
		t.Fatalf("qualified cross-namespace read: %#v", result)
	}
	for _, query := range []string{
		"CREATE TABLE rejected_constraint (id INT, PRIMARY KEY (id))",
		"CREATE TABLE rejected_option (id INT) ENGINE=InnoDB",
	} {
		if result := client.query(query); result.err == "" {
			t.Fatalf("recognized unsupported table definition succeeded: query=%q result=%#v", query, result)
		}
	}
	for _, table := range []string{"rejected_constraint", "rejected_option"} {
		if result := client.query("SELECT * FROM " + table); result.err == "" {
			t.Fatalf("unsupported definition left a durable table: table=%q result=%#v", table, result)
		}
	}
	if err := client.close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("stop: %#v", result)
	}

	process, address = startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	reopened := newWireClient(t, address, "admin", "namespace-secret")
	defer reopened.close()
	if result := reopened.query("USE alpha"); result.err != "" {
		t.Fatalf("use alpha after restart: %#v", result)
	}
	if result := reopened.query("SELECT * FROM local_rows"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "1,Ada" {
		t.Fatalf("current namespace durable row: %#v", result)
	}
	if result := reopened.query("SELECT * FROM beta.cross_rows"); result.err != "" || len(result.rows) != 1 || strings.Join(result.rows[0], ",") != "2,Grace" {
		t.Fatalf("qualified durable row: %#v", result)
	}
}

func TestMySQLCRUDStatementsAreAtomicAndPreparedExecutionMatchesText(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("crud-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "crud-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE users (id INT, name VARCHAR(32))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}
	inserted := client.query("INSERT INTO users (id, name) VALUES (1, 'Ada'), (2, 'Grace')")
	if inserted.err != "" || inserted.affected != 2 {
		t.Fatalf("multi-row insert: %#v", inserted)
	}
	selected := client.query("SELECT id, name FROM users WHERE id = 2")
	if selected.err != "" || strings.Join(selected.columns, ",") != "id,name" || len(selected.metadata) != 2 || selected.metadata[0].typ != 0x03 || selected.metadata[1].typ != 0xfd || selected.metadata[0].schema != "app" || selected.metadata[0].table != "users" || len(selected.rows) != 1 || strings.Join(selected.rows[0], ",") != "2,Grace" {
		t.Fatalf("projected lookup metadata and row: %#v", selected)
	}
	preparedLookup := client.prepare("SELECT id FROM users WHERE id = ?")
	if preparedLookup.err != "" || len(preparedLookup.metadata) != 1 || preparedLookup.metadata[0].typ != 0x03 || preparedLookup.metadata[0].schema != "app" || preparedLookup.metadata[0].table != "users" {
		t.Fatalf("prepared lookup metadata: %#v", preparedLookup)
	}
	if result := client.executePreparedValues(preparedLookup.id, []preparedParameter{{typ: 0x08, value: []byte{2, 0, 0, 0, 0, 0, 0, 0}}}); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "2" {
		t.Fatalf("prepared lookup: %#v", result)
	}
	client.closePrepared(preparedLookup.id)
	updated := client.query("UPDATE users SET name = 'Grace Hopper' WHERE id = 2")
	if updated.err != "" || updated.affected != 1 {
		t.Fatalf("text update: %#v", updated)
	}
	if unchanged := client.query("UPDATE users SET name = 'Grace Hopper' WHERE id = 2"); unchanged.err != "" || unchanged.affected != 0 {
		t.Fatalf("unchanged update affected rows: %#v", unchanged)
	}
	if failed := client.query("UPDATE users SET name = 'Broken', missing = 'value' WHERE id = 1"); failed.err == "" {
		t.Fatalf("invalid update succeeded: %#v", failed)
	}
	if rows := client.query("SELECT * FROM users WHERE id = 1"); rows.err != "" || len(rows.rows) != 1 || strings.Join(rows.rows[0], ",") != "1,Ada" {
		t.Fatalf("failed update leaked a partial row change: %#v", rows)
	}
	if failed := client.query("INSERT INTO users VALUES (3, 'Linus'), (4)"); failed.err == "" {
		t.Fatalf("invalid multi-row insert succeeded: %#v", failed)
	}
	if failed := client.query("INSERT INTO users VALUES (3, 'Linus'),"); failed.err == "" {
		t.Fatalf("trailing-comma insert succeeded: %#v", failed)
	}
	if rows := client.query("SELECT * FROM users"); rows.err != "" || len(rows.rows) != 2 {
		t.Fatalf("failed insert leaked rows: %#v", rows)
	}

	statement := client.prepare("INSERT INTO users (id, name) VALUES (?, ?)")
	if statement.err != "" {
		t.Fatalf("prepare DML: %#v", statement)
	}
	preparedInsert := client.executePreparedValues(statement.id, []preparedParameter{
		{typ: 0x08, value: []byte{3, 0, 0, 0, 0, 0, 0, 0}},
		{typ: 0xfd, value: []byte("Linus")},
	})
	if preparedInsert.err != "" || preparedInsert.affected != 1 {
		t.Fatalf("prepared insert: %#v", preparedInsert)
	}
	client.closePrepared(statement.id)

	statement = client.prepare("UPDATE users SET name = ? WHERE id = ?")
	if statement.err != "" {
		t.Fatalf("prepare update: %#v", statement)
	}
	preparedUpdate := client.executePreparedValues(statement.id, []preparedParameter{
		{typ: 0xfd, value: []byte("Ada Lovelace")},
		{typ: 0x08, value: []byte{1, 0, 0, 0, 0, 0, 0, 0}},
	})
	if preparedUpdate.err != "" || preparedUpdate.affected != 1 {
		t.Fatalf("prepared update: %#v", preparedUpdate)
	}
	client.closePrepared(statement.id)

	deleted := client.query("DELETE FROM users WHERE id = 2")
	if deleted.err != "" || deleted.affected != 1 {
		t.Fatalf("delete: %#v", deleted)
	}
	statement = client.prepare("DELETE FROM users WHERE id = ?")
	if statement.err != "" {
		t.Fatalf("prepare delete: %#v", statement)
	}
	preparedDelete := client.executePreparedValues(statement.id, []preparedParameter{
		{typ: 0x08, value: []byte{3, 0, 0, 0, 0, 0, 0, 0}},
	})
	if preparedDelete.err != "" || preparedDelete.affected != 1 {
		t.Fatalf("prepared delete: %#v", preparedDelete)
	}
	client.closePrepared(statement.id)
	if rows := client.query("SELECT * FROM users"); rows.err != "" || len(rows.rows) != 1 || strings.Join(rows.rows[0], ",") != "1,Ada Lovelace" {
		t.Fatalf("CRUD final rows: %#v", rows)
	}
}

func TestMySQLPreparedStatementsUseBinaryRowsAndResetSafely(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("prepared-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, mysqlAddress := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, mysqlAddress, "admin", "prepared-secret")
	defer client.close()

	text := client.query("SELECT 7")
	statement := client.prepare("SELECT 7")
	if text.err != "" || statement.err != "" {
		t.Fatalf("text=%#v prepare=%#v", text, statement)
	}
	malformed := []byte{0x17, byte(statement.id), byte(statement.id >> 8), byte(statement.id >> 16), byte(statement.id >> 24), 0, 1, 0, 0, 0, 0}
	writeWirePacket(t, client.conn, 0, malformed)
	assertWireError(t, readWirePacket(t, client.conn), 1210, "HY000")
	binaryResult := client.executePrepared(statement.id)
	if binaryResult.err != "" || len(binaryResult.rows) != 1 || len(binaryResult.rows[0]) != 1 || binaryResult.rows[0][0] != "7" || len(binaryResult.metadata) != 1 || len(text.metadata) != 1 || binaryResult.metadata[0] != text.metadata[0] {
		t.Fatalf("prepared binary result differs from text: text=%#v binary=%#v", text, binaryResult)
	}

	bound := client.prepare("SELECT ?")
	if bound.err != "" {
		t.Fatalf("prepare bound statement: %#v", bound)
	}
	if result := client.executePreparedValues(bound.id, []preparedParameter{{typ: 0xfd, value: []byte("Ada")}}); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "Ada" {
		t.Fatalf("bound result: %#v", result)
	}
	uint64Value := make([]byte, 8)
	binary.LittleEndian.PutUint64(uint64Value, math.MaxUint64)
	if result := client.executePreparedValues(bound.id, []preparedParameter{{typ: 0x08, unsigned: true, value: uint64Value}}); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "18446744073709551615" {
		t.Fatalf("unsigned bound result: %#v", result)
	}
	floatValue := make([]byte, 8)
	binary.LittleEndian.PutUint64(floatValue, math.Float64bits(1.5))
	if result := client.executePreparedValues(bound.id, []preparedParameter{{typ: 0x05, value: floatValue}}); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1.5" {
		t.Fatalf("float bound result: %#v", result)
	}
	longValue := strings.Repeat("x", 16*1024*1024)
	client.sendLongData(bound.id, 0, []byte(longValue[:8*1024*1024]))
	client.sendLongData(bound.id, 0, []byte(longValue[8*1024*1024:]))
	if result := client.executePreparedValues(bound.id, []preparedParameter{{typ: 0xfd, long: true}}); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != longValue {
		t.Fatalf("long-data result: err=%q rows=%d", result.err, len(result.rows))
	}
	if err := client.resetPrepared(bound.id); err != nil {
		t.Fatal(err)
	}
	if result := client.executePreparedValues(bound.id, []preparedParameter{{typ: 0xfd, value: []byte("fresh")}}); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "fresh" {
		t.Fatalf("statement reset did not clear long data: %#v", result)
	}
	client.closePrepared(bound.id)
	if result := client.executePrepared(bound.id); result.err == "" {
		t.Fatalf("closed statement executed: %#v", result)
	}

	if result := client.query("SET autocommit = 0"); result.err != "" {
		t.Fatalf("disable autocommit before reset: %#v", result)
	}
	for _, query := range []string{
		"CREATE DATABASE reset_rollback",
		"USE reset_rollback",
		"CREATE TABLE pending (id INT)",
		"BEGIN",
		"INSERT INTO pending VALUES (1)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("transaction setup %s: %#v", query, result)
		}
	}
	resetStatement := client.prepare("SELECT 1")
	if resetStatement.err != "" {
		t.Fatalf("prepare before connection reset: %#v", resetStatement)
	}
	if err := client.reset(); err != nil {
		t.Fatal(err)
	}
	if result := client.query("USE reset_rollback"); result.err != "" {
		t.Fatalf("connection reset lost committed namespace: %#v", result)
	}
	if result := client.query("SELECT * FROM pending"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("connection reset did not roll back work: %#v", result)
	}
	if result := client.query("INSERT INTO pending VALUES (2)"); result.err != "" {
		t.Fatalf("connection reset did not restore autocommit: %#v", result)
	}
	probe := newWireClient(t, mysqlAddress, "admin", "prepared-secret")
	defer probe.close()
	if result := probe.query("USE reset_rollback"); result.err != "" {
		t.Fatalf("probe USE after reset: %#v", result)
	}
	if result := probe.query("SELECT * FROM pending"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "2" {
		t.Fatalf("connection reset left autocommit disabled: %#v", result)
	}
	if result := client.executePrepared(resetStatement.id); result.err == "" {
		t.Fatalf("connection reset retained prepared statement: %#v", result)
	}
	if result := client.query("SELECT 1"); result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("connection reset lost authentication: %#v", result)
	}
}

func TestMySQLPreparedStatementCountIsBounded(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("prepared-limit-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, mysqlAddress := startMySQLServer(t, runner, directory, "--max-prepared-stmt-count", "1")
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, mysqlAddress, "admin", "prepared-limit-secret")
	defer client.close()
	first := client.prepare("SELECT 1")
	if first.err != "" {
		t.Fatalf("first prepared statement: %#v", first)
	}
	if second := client.prepare("SELECT 2"); second.err == "" {
		t.Fatalf("prepared statement limit was not enforced: %#v", second)
	}
	client.closePrepared(first.id)
	if replacement := client.prepare("SELECT 3"); replacement.err != "" {
		t.Fatalf("closing statement did not release limit: %#v", replacement)
	}
}

func TestMySQLSessionCeilingRejectsAdditionalConnections(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory, "--max-connections=1")
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()
	second, err := sql.Open("mysql", "admin:lifecycle-secret@tcp("+address+")/")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	context, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := second.PingContext(context); err == nil {
		t.Fatal("authenticated session beyond max_connections was admitted")
	}
}

func TestMySQLTLSAuthenticationTextLiteralAndProtocolFailures(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("secure-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	certificate, key := testTLSCertificate(t)
	address := freeAddress(t)
	process, err := runner.Start(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", address, "--tls-certificate-file", certificate, "--tls-private-key-file", key, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	var ready map[string]any
	if err := process.NextJSONEvent(context.Background(), &ready); err != nil || ready["state"] != "ready" {
		t.Fatalf("ready: %#v %v", ready, err)
	}

	client := newTLSWireClient(t, address, "admin", "secure-password")
	defer client.close()
	writeWirePacket(t, client.conn, 0, append([]byte{0x03}, "SELECT 'Ada'"...))
	if count := readWirePacket(t, client.conn); len(count) != 1 || count[0] != 1 {
		t.Fatalf("column count: %x", count)
	}
	definition := readWirePacket(t, client.conn)
	column, ok := parseColumnDefinition(definition)
	if !ok || column.catalog != "def" || column.schema != "" || column.table != "" || column.originalTable != "" || column.name != "'Ada'" || column.originalName != "" || column.characterSet != 255 || column.length != 12 || column.typ != 0xfd || column.flags != 1 || column.decimals != 0 {
		t.Fatalf("literal ColumnDefinition41 = %#v (raw %x)", column, definition)
	}
	_ = readWirePacket(t, client.conn) // column terminator
	if row := readWirePacket(t, client.conn); string(row) != "\x03Ada" {
		t.Fatalf("literal row = %x", row)
	}
	_ = readWirePacket(t, client.conn) // row terminator

	writeWirePacket(t, client.conn, 0, append([]byte{0x03}, "SELECT 1"...))
	if count := readWirePacket(t, client.conn); len(count) != 1 || count[0] != 1 {
		t.Fatalf("integer column count: %x", count)
	}
	integerDefinition := readWirePacket(t, client.conn)
	integerColumn, ok := parseColumnDefinition(integerDefinition)
	if !ok || integerColumn.characterSet != 63 || integerColumn.length != 1 || integerColumn.typ != 0x08 || integerColumn.flags != 0x81 || integerColumn.decimals != 0 {
		t.Fatalf("integer literal ColumnDefinition41 = %#v (raw %x)", integerColumn, integerDefinition)
	}
	_ = readWirePacket(t, client.conn)
	if row := readWirePacket(t, client.conn); string(row) != "\x011" {
		t.Fatalf("integer literal row = %x", row)
	}
	_ = readWirePacket(t, client.conn)

	writeWirePacket(t, client.conn, 0, append([]byte{0x03}, "SELECT NULL"...))
	if count := readWirePacket(t, client.conn); len(count) != 1 || count[0] != 1 {
		t.Fatalf("NULL column count: %x", count)
	}
	nullDefinition := readWirePacket(t, client.conn)
	nullColumn, ok := parseColumnDefinition(nullDefinition)
	if !ok || nullColumn.characterSet != 63 || nullColumn.length != 0 || nullColumn.typ != 0x06 || nullColumn.flags != 0x80 || nullColumn.decimals != 0 {
		t.Fatalf("NULL literal ColumnDefinition41 = %#v (raw %x)", nullColumn, nullDefinition)
	}
	_ = readWirePacket(t, client.conn)
	if row := readWirePacket(t, client.conn); len(row) != 1 || row[0] != 0xfb {
		t.Fatalf("NULL literal row = %x", row)
	}
	_ = readWirePacket(t, client.conn)

	writeWirePacket(t, client.conn, 0, []byte{0x7f})
	assertWireError(t, readWirePacket(t, client.conn), 1047, "08S01")

	plain, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	greeting := readWirePacket(t, plain)
	_, capabilities := greetingNonceAndCapabilities(t, greeting)
	unsupported := capabilities | (1 << 5) // CLIENT_COMPRESS was not negotiated.
	response := handshakeResponse(unsupported, "admin", nil)
	writeWirePacket(t, plain, 1, response)
	assertWireError(t, readWirePacket(t, plain), 1043, "08S01")

	client.close()
	driver, err := sql.Open("mysql", "admin:secure-password@tcp("+address+")/?tls=skip-verify")
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	var value string
	if err := driver.QueryRowContext(context.Background(), "SELECT 'Ada'").Scan(&value); err != nil || value != "Ada" {
		t.Fatalf("go-sql-driver text query: value=%q err=%v", value, err)
	}
	statement, err := driver.PrepareContext(context.Background(), "SELECT ?")
	if err != nil {
		t.Fatalf("go-sql-driver prepare: %v", err)
	}
	defer statement.Close()
	if err := statement.QueryRowContext(context.Background(), "Ada").Scan(&value); err != nil || value != "Ada" {
		t.Fatalf("go-sql-driver prepared query: value=%q err=%v", value, err)
	}
}

func TestMySQLCatalogReturnsCanonicalCreateDefinitions(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("catalog-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, mysqlAddress := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, mysqlAddress, "admin", "catalog-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE zeta",
		"CREATE DATABASE alpha",
		"USE zeta",
		"CREATE TABLE zebra (name VARCHAR(32), id INT)",
		"CREATE TABLE apple (value DECIMAL(10,2))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	databases := client.query("SHOW DATABASES")
	if databases.err != "" || strings.Join(databases.columns, ",") != "Database" || len(databases.rows) != 3 || databases.rows[0][0] != "alpha" || databases.rows[1][0] != "information_schema" || databases.rows[2][0] != "zeta" {
		t.Fatalf("sorted databases: %#v", databases)
	}
	tables := client.query("SHOW TABLES")
	if tables.err != "" || strings.Join(tables.columns, ",") != "Tables_in_zeta" || len(tables.rows) != 2 || tables.rows[0][0] != "apple" || tables.rows[1][0] != "zebra" {
		t.Fatalf("sorted tables: %#v", tables)
	}

	databaseDefinition := client.query("SHOW CREATE DATABASE zeta")
	if databaseDefinition.err != "" || strings.Join(databaseDefinition.columns, ",") != "Database,Create Database" || len(databaseDefinition.rows) != 1 || strings.Join(databaseDefinition.rows[0], "\n") != "zeta\nCREATE DATABASE `zeta`" {
		t.Fatalf("canonical database definition: %#v", databaseDefinition)
	}
	tableDefinition := client.query("SHOW CREATE TABLE zeta.zebra")
	expectedTable := "zebra\nCREATE TABLE `zebra` (\n  `name` VARCHAR(32),\n  `id` INT\n)"
	if tableDefinition.err != "" || strings.Join(tableDefinition.columns, ",") != "Table,Create Table" || len(tableDefinition.rows) != 1 || strings.Join(tableDefinition.rows[0], "\n") != expectedTable {
		t.Fatalf("canonical table definition: %#v", tableDefinition)
	}
	decimalDefinition := client.query("SHOW CREATE TABLE apple")
	if decimalDefinition.err != "" || len(decimalDefinition.rows) != 1 || decimalDefinition.rows[0][1] != "CREATE TABLE `apple` (\n  `value` DECIMAL(10,2)\n)" {
		t.Fatalf("parenthesized type definition: %#v", decimalDefinition)
	}
}

func TestMySQLMetadataIsHonestEscapedAndCommittedConsistent(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("metadata-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, mysqlAddress := startMySQLServer(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()

	client := newWireClient(t, mysqlAddress, "admin", "metadata-secret")
	defer client.close()
	for _, query := range []string{
		"CREATE DATABASE `odd``name`",
		"USE `odd``name`",
		"CREATE TABLE `part.name` (`id` INT, legacy)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	database := client.query("SHOW CREATE DATABASE `odd``name`")
	if database.err != "" || len(database.rows) != 1 || database.rows[0][1] != "CREATE DATABASE `odd``name`" {
		t.Fatalf("escaped database definition: %#v", database)
	}
	table := client.query("SHOW CREATE TABLE `part.name`")
	if table.err == "" || !strings.Contains(table.err, "type for column") || strings.Contains(table.err, "TEXT") {
		t.Fatalf("unknown type was fabricated or hidden: %#v", table)
	}

	columns := client.query("SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE FROM information_schema.columns")
	if columns.err != "" || strings.Join(columns.columns, ",") != "COLUMN_NAME,DATA_TYPE,COLUMN_TYPE" {
		t.Fatalf("information_schema.columns projection: %#v", columns)
	}
	var unknown []string
	for _, row := range columns.rows {
		if row[0] == "legacy" {
			unknown = row
		}
	}
	if unknown == nil || unknown[1] != "" || unknown[2] != "" {
		t.Fatalf("unknown type metadata: %#v", columns)
	}

	if result := client.query("USE information_schema"); result.err != "" {
		t.Fatalf("use information_schema: %#v", result)
	}
	views := client.query("SHOW TABLES")
	if views.err != "" || len(views.rows) != 3 || views.rows[0][0] != "schemata" || views.rows[1][0] != "tables" || views.rows[2][0] != "columns" {
		t.Fatalf("information_schema views: %#v", views)
	}
	if result := client.query("CREATE TABLE rejected (id INT)"); result.err == "" || !strings.Contains(result.err, "read-only") {
		t.Fatalf("information_schema mutation: %#v", result)
	}
	if result := client.query("SELECT * FROM information_schema.not_a_view"); result.err == "" || !strings.Contains(result.err, "unsupported information_schema view") {
		t.Fatalf("unsupported metadata behavior: %#v", result)
	}

	if result := client.query("USE `odd``name`"); result.err != "" || client.query("BEGIN").err != "" {
		t.Fatalf("begin transaction: %#v", result)
	}
	if result := client.query("CREATE TABLE pending (id INT)"); result.err != "" {
		t.Fatalf("create pending table: %#v", result)
	}
	metadata := client.query("SELECT TABLE_NAME FROM information_schema.tables")
	found := false
	for _, row := range metadata.rows {
		if row[0] == "pending" {
			found = true
		}
	}
	if !found {
		t.Fatalf("implicitly committed table missing from metadata: %#v", metadata)
	}
	if result := client.query("COMMIT"); result.err != "" {
		t.Fatalf("commit: %#v", result)
	}
	metadata = client.query("SELECT TABLE_NAME FROM information_schema.tables")
	found = false
	for _, row := range metadata.rows {
		if row[0] == "pending" {
			found = true
		}
	}
	if !found {
		t.Fatalf("committed table missing from metadata: %#v", metadata)
	}
}

func startMySQLServer(t *testing.T, runner blackbox.Runner, directory string, extraArguments ...string) (*blackbox.Process, string) {
	process, address, _ := startMySQLServerWithReady(t, runner, directory, extraArguments...)
	return process, address
}

func startMySQLServerWithReady(t *testing.T, runner blackbox.Runner, directory string, extraArguments ...string) (*blackbox.Process, string, bool) {
	t.Helper()
	diagnostics := freeAddress(t)
	mysqlAddress := freeAddress(t)
	arguments := []string{"serve", "--data-directory", directory, "--mysql-listen-address", mysqlAddress, "--diagnostics-listen-address", diagnostics}
	arguments = append(arguments, extraArguments...)
	arguments = append(arguments, "--format=json")
	process, err := runner.Start(context.Background(), arguments...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var event struct {
		State     string `json:"state"`
		Recovered bool   `json:"recovered"`
	}
	if err := process.NextJSONEvent(ctx, &event); err != nil || event.State != "ready" {
		process.Crash()
		result := process.Wait()
		t.Fatalf("wait for ready event: %v; result=%#v", err, result)
	}
	return process, mysqlAddress, event.Recovered
}

func TestServingInstanceOwnsDirectoryRejectsDamageAndRollsBackOnStop(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("shutdown-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}

	process, address := startMySQLServer(t, runner, directory)
	second := runner.Run(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", freeAddress(t), "--format=json")
	if second.ExitCode != 1 || !strings.Contains(second.Stdout, "already in use") {
		t.Fatalf("second owner: %#v", second)
	}

	client := newWireClient(t, address, "admin", "shutdown-secret")
	for _, query := range []string{
		"CREATE DATABASE interrupted",
		"USE interrupted",
		"CREATE TABLE pending (id INT)",
		"BEGIN",
		"INSERT INTO pending VALUES (1)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("shutdown transaction setup %s: %#v", query, result)
		}
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("graceful shutdown: %#v", result)
	}
	_ = client.conn.Close()

	process, address = startMySQLServer(t, runner, directory)
	client = newWireClient(t, address, "admin", "shutdown-secret")
	if result := client.query("USE interrupted"); result.err != "" {
		t.Fatalf("committed database missing after shutdown: %#v", result)
	}
	if result := client.query("SELECT * FROM pending"); result.err != "" || len(result.rows) != 0 {
		t.Fatalf("uncommitted row survived shutdown: %#v", result)
	}
	if err := client.close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("restart shutdown: %#v", result)
	}

	damaged := filepath.Join(t.TempDir(), "damaged-instance")
	if result := runner.Run(context.Background(), "init", damaged, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize damaged fixture: %#v", result)
	}
	if err := os.Remove(filepath.Join(damaged, "catalog.json")); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "serve", "--data-directory", damaged, "--mysql-listen-address", freeAddress(t), "--format=json"); result.ExitCode != 1 || !strings.Contains(result.Stdout, "catalog") {
		t.Fatalf("damaged directory: %#v", result)
	}
}

func TestCrashRecoveryPreservesDurableCommitAndDropsInFlightTransaction(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("crash-recovery-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}

	process, address, recovered := startMySQLServerWithReady(t, runner, directory)
	if recovered {
		t.Fatal("first start reported recovery")
	}
	client := newWireClient(t, address, "admin", "crash-recovery-secret")
	for _, query := range []string{
		"CREATE DATABASE crash_recovery",
		"USE crash_recovery",
		"CREATE TABLE entries (id INT)",
		"INSERT INTO entries VALUES (1)",
		"BEGIN",
		"INSERT INTO entries VALUES (2)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("setup %s: %#v", query, result)
		}
	}
	if err := process.Crash(); err != nil {
		t.Fatal(err)
	}
	crash := process.Wait()
	if crash.ExitCode == 0 {
		t.Fatalf("crash unexpectedly exited successfully: %#v", crash)
	}
	_ = client.conn.Close()

	process, address, recovered = startMySQLServerWithReady(t, runner, directory)
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	if !recovered {
		t.Fatal("restart did not report recovery")
	}
	client = newWireClient(t, address, "admin", "crash-recovery-secret")
	defer client.close()
	rows := client.query("USE crash_recovery")
	if rows.err != "" {
		t.Fatalf("reopen database: %#v", rows)
	}
	rows = client.query("SELECT * FROM entries")
	if rows.err != "" || len(rows.rows) != 1 || rows.rows[0][0] != "1" {
		t.Fatalf("recovered rows: %#v", rows)
	}
}

func TestServeEmitsTerminalOperatorResult(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("result-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, _ := startMySQLServer(t, runner, directory)
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	result := process.Wait()
	if result.ExitCode != 0 {
		t.Fatalf("graceful shutdown: %#v", result)
	}
	var readyOperationID, resultOperationID string
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid serve output line %q: %v", line, err)
		}
		if event["schema"] == "database.lifecycle/v1" && event["state"] == "ready" {
			readyOperationID, _ = event["operation_id"].(string)
		}
		if event["schema"] == "database.operator.result/v1" && event["operation"] == "serve" {
			if event["success"] != true || event["exit_class"] != "success" {
				t.Fatalf("terminal serve result = %#v", event)
			}
			resultOperationID, _ = event["operation_id"].(string)
		}
	}
	if readyOperationID == "" || resultOperationID == "" || readyOperationID != resultOperationID {
		t.Fatalf("serve result did not correlate lifecycle progress: ready=%q result=%q output=%q", readyOperationID, resultOperationID, result.Stdout)
	}
}

type wireResult struct {
	columns  []string
	rows     [][]string
	metadata []wireColumn
	affected uint64
	err      string
}

type preparedStatement struct {
	id       uint32
	metadata []wireColumn
	err      string
}

type preparedParameter struct {
	typ      byte
	unsigned bool
	null     bool
	long     bool
	value    []byte
}

type wireClient struct {
	t    *testing.T
	conn net.Conn
	seq  byte
}

type wireColumn struct {
	catalog, schema, table, originalTable, name, originalName string
	characterSet                                              uint16
	length                                                    uint32
	typ                                                       byte
	flags                                                     uint16
	decimals                                                  byte
}

func newTLSWireClient(t *testing.T, address, username, password string) *wireClient {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	greeting := readWirePacket(t, connection)
	nonce, capabilities := greetingNonceAndCapabilities(t, greeting)
	if capabilities&(1<<11) == 0 {
		connection.Close()
		t.Fatal("server did not advertise CLIENT_SSL")
	}
	sslRequest := make([]byte, 32)
	binary.LittleEndian.PutUint32(sslRequest[:4], capabilities)
	sslRequest[8] = 45
	writeWirePacket(t, connection, 1, sslRequest)
	tlsConnection := tls.Client(connection, &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}) // #nosec G402 -- ephemeral black-box certificate
	if err := tlsConnection.Handshake(); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	writeWirePacket(t, tlsConnection, 2, handshakeResponse(capabilities, username, cachingSHA2Token(password, nonce)))
	if auth := readWirePacket(t, tlsConnection); len(auth) != 2 || auth[0] != 0x01 || auth[1] != 0x04 {
		tlsConnection.Close()
		t.Fatalf("authentication did not require TLS full exchange: %x", auth)
	}
	writeWirePacket(t, tlsConnection, 4, append([]byte(password), 0))
	if auth := readWirePacket(t, tlsConnection); len(auth) == 0 || auth[0] != 0x00 {
		tlsConnection.Close()
		t.Fatalf("TLS authentication failed: %x", auth)
	}
	return &wireClient{t: t, conn: tlsConnection}
}

func greetingNonceAndCapabilities(t *testing.T, payload []byte) ([]byte, uint32) {
	t.Helper()
	if len(payload) == 0 || payload[0] != 0x0a {
		t.Fatalf("invalid greeting: %x", payload)
	}
	versionEnd := bytesIndex(payload[1:], 0) + 1
	position := versionEnd + 1 + 4
	if position+8 >= len(payload) {
		t.Fatalf("truncated greeting: %x", payload)
	}
	nonce := append([]byte(nil), payload[position:position+8]...)
	position += 9
	lower := binary.LittleEndian.Uint16(payload[position : position+2])
	position += 2 + 1 + 2
	upper := binary.LittleEndian.Uint16(payload[position : position+2])
	position += 2
	authLength := int(payload[position])
	position += 1 + 10
	remaining := authLength - 1 - 8
	if remaining < 0 || position+remaining > len(payload) {
		t.Fatalf("malformed greeting nonce: %x", payload)
	}
	nonce = append(nonce, payload[position:position+remaining]...)
	return nonce, uint32(lower) | uint32(upper)<<16
}

func handshakeResponse(capabilities uint32, username string, token []byte) []byte {
	response := make([]byte, 0, 64+len(username)+len(token))
	response = append(response, byte(capabilities), byte(capabilities>>8), byte(capabilities>>16), byte(capabilities>>24))
	response = append(response, 0, 0, 0, 0, 45)
	response = append(response, make([]byte, 23)...)
	response = append(response, username...)
	response = append(response, 0, byte(len(token)))
	response = append(response, token...)
	response = append(response, []byte("caching_sha2_password")...)
	return append(response, 0)
}

func cachingSHA2Token(password string, nonce []byte) []byte {
	stage1 := sha256.Sum256([]byte(password))
	stage2 := sha256.Sum256(stage1[:])
	scramble := sha256.Sum256(append(append([]byte{}, stage2[:]...), nonce...))
	token := make([]byte, len(stage1))
	for index := range token {
		token[index] = stage1[index] ^ scramble[index]
	}
	return token
}

func parseColumnDefinition(payload []byte) (wireColumn, bool) {
	values := [6]string{}
	offset := 0
	for index := range values {
		value, next, ok := readLengthString(payload, offset)
		if !ok {
			return wireColumn{}, false
		}
		values[index], offset = value, next
	}
	if offset+13 != len(payload) || payload[offset] != 0x0c {
		return wireColumn{}, false
	}
	return wireColumn{catalog: values[0], schema: values[1], table: values[2], originalTable: values[3], name: values[4], originalName: values[5], characterSet: binary.LittleEndian.Uint16(payload[offset+1 : offset+3]), length: binary.LittleEndian.Uint32(payload[offset+3 : offset+7]), typ: payload[offset+7], flags: binary.LittleEndian.Uint16(payload[offset+8 : offset+10]), decimals: payload[offset+10]}, true
}

func assertWireError(t *testing.T, payload []byte, code uint16, state string) {
	t.Helper()
	if len(payload) < 9 || payload[0] != 0xff || binary.LittleEndian.Uint16(payload[1:3]) != code || payload[3] != '#' || string(payload[4:9]) != state {
		t.Fatalf("error packet = %x, want code %d state %s", payload, code, state)
	}
}

func testTLSCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "database test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificate, privateKey := filepath.Join(directory, "certificate.pem"), filepath.Join(directory, "key.pem")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificate, privateKey
}

func newWireClient(t *testing.T, address, username, password string) *wireClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	nonce, capabilities := greetingNonceAndCapabilities(t, readWirePacket(t, conn))
	writeWirePacket(t, conn, 1, handshakeResponse(capabilities, username, cachingSHA2Token(password, nonce)))
	if auth := readWirePacket(t, conn); len(auth) != 2 || auth[0] != 0x01 || auth[1] != 0x04 {
		conn.Close()
		t.Fatalf("authentication did not require secure exchange: %x", auth)
	}
	writeWirePacket(t, conn, 3, []byte{0x02})
	publicKeyPacket := readWirePacket(t, conn)
	if len(publicKeyPacket) < 2 || publicKeyPacket[0] != 0x01 {
		conn.Close()
		t.Fatalf("authentication public key: %x", publicKeyPacket)
	}
	block, _ := pem.Decode(publicKeyPacket[1:])
	if block == nil {
		conn.Close()
		t.Fatalf("invalid authentication public key: %q", publicKeyPacket[1:])
	}
	publicKey, err := parseRSAPublicKey(block.Bytes)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	plain := append([]byte(password), 0)
	for index := range plain {
		plain[index] ^= nonce[index%len(nonce)]
	}
	encrypted, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, publicKey, plain, nil)
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	writeWirePacket(t, conn, 5, encrypted)
	if auth := readWirePacket(t, conn); len(auth) == 0 || auth[0] != 0x00 {
		conn.Close()
		t.Fatalf("authentication failed: %x", auth)
	}
	return &wireClient{t: t, conn: conn}
}

func parseRSAPublicKey(der []byte) (*rsa.PublicKey, error) {
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("authentication key is %T, not RSA", key)
	}
	return rsaKey, nil
}

func (c *wireClient) query(query string) wireResult {
	writeWirePacket(c.t, c.conn, 0, append([]byte{0x03}, query...))
	return c.readResult()
}

func (c *wireClient) prepare(query string) preparedStatement {
	writeWirePacket(c.t, c.conn, 0, append([]byte{0x16}, query...))
	payload := readWirePacket(c.t, c.conn)
	if len(payload) < 12 || payload[0] != 0 {
		return preparedStatement{err: fmt.Sprintf("prepare response %x", payload)}
	}
	parameters := int(binary.LittleEndian.Uint16(payload[7:9]))
	columns := int(binary.LittleEndian.Uint16(payload[5:7]))
	metadata := make([]wireColumn, 0, columns)
	for index, count := range []int{parameters, columns} {
		for range count {
			column, ok := parseColumnDefinition(readWirePacket(c.t, c.conn))
			if !ok {
				return preparedStatement{err: "malformed prepared metadata"}
			}
			if index == 1 {
				metadata = append(metadata, column)
			}
		}
		if count > 0 {
			if terminator := readWirePacket(c.t, c.conn); len(terminator) == 0 || terminator[0] != 0xfe {
				return preparedStatement{err: fmt.Sprintf("malformed prepared metadata terminator %x", terminator)}
			}
		}
	}
	return preparedStatement{id: binary.LittleEndian.Uint32(payload[1:5]), metadata: metadata}
}

func (c *wireClient) executePrepared(id uint32) wireResult {
	payload := []byte{0x17, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), 0, 1, 0, 0, 0}
	writeWirePacket(c.t, c.conn, 0, payload)
	return c.readPreparedResult()
}

func (c *wireClient) executePreparedValues(id uint32, parameters []preparedParameter) wireResult {
	payload := []byte{0x17, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), 0, 1, 0, 0, 0}
	nullBytes := make([]byte, (len(parameters)+7)/8)
	for index, parameter := range parameters {
		if parameter.null {
			nullBytes[index/8] |= 1 << (index % 8)
		}
	}
	payload = append(payload, nullBytes...)
	payload = append(payload, 1)
	for _, parameter := range parameters {
		unsigned := byte(0)
		if parameter.unsigned {
			unsigned = 0x80
		}
		payload = append(payload, parameter.typ, unsigned)
	}
	for _, parameter := range parameters {
		if !parameter.null && !parameter.long {
			switch parameter.typ {
			case 0x0f, 0xfd, 0xfe, 0xfc, 0xfb, 0xfa, 0xf9, 0xf5, 0xf6:
				payload = append(payload, lengthEncodedWire(parameter.value)...)
			default:
				payload = append(payload, parameter.value...)
			}
		}
	}
	writeWirePacket(c.t, c.conn, 0, payload)
	return c.readPreparedResult()
}

func lengthEncodedWire(value []byte) []byte {
	if len(value) < 251 {
		return append([]byte{byte(len(value))}, value...)
	}
	if len(value) <= 0xffff {
		return append([]byte{0xfc, byte(len(value)), byte(len(value) >> 8)}, value...)
	}
	return append([]byte{0xfd, byte(len(value)), byte(len(value) >> 8), byte(len(value) >> 16)}, value...)
}

func (c *wireClient) sendLongData(id uint32, parameter uint16, value []byte) {
	payload := []byte{0x18, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), byte(parameter), byte(parameter >> 8)}
	writeWirePacket(c.t, c.conn, 0, append(payload, value...))
}

func (c *wireClient) resetPrepared(id uint32) error {
	payload := []byte{0x1a, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24)}
	writeWirePacket(c.t, c.conn, 0, payload)
	if result := readWirePacket(c.t, c.conn); len(result) == 0 || result[0] != 0 {
		return fmt.Errorf("prepared reset response %x", result)
	}
	return nil
}

func (c *wireClient) closePrepared(id uint32) {
	payload := []byte{0x19, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24)}
	writeWirePacket(c.t, c.conn, 0, payload)
}

func (c *wireClient) reset() error {
	writeWirePacket(c.t, c.conn, 0, []byte{0x1f})
	if payload := readWirePacket(c.t, c.conn); len(payload) == 0 || payload[0] != 0 {
		return fmt.Errorf("reset response %x", payload)
	}
	return nil
}

func (c *wireClient) close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	writeWirePacket(c.t, c.conn, 0, []byte{0x01})
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *wireClient) readResult() wireResult {
	result, complete := c.readResultHeader()
	if complete {
		return result
	}
	columnCount := len(result.columns)
	for {
		row := readWirePacket(c.t, c.conn)
		if len(row) == 0 {
			return wireResult{err: "empty row packet"}
		}
		if row[0] == 0xfe && len(row) < 9 {
			break
		}
		values := make([]string, 0, columnCount)
		offset := 0
		for i := 0; i < columnCount; i++ {
			value, next, valid := readLengthString(row, offset)
			if !valid {
				return wireResult{err: fmt.Sprintf("malformed row %x", row)}
			}
			values = append(values, value)
			offset = next
		}
		result.rows = append(result.rows, values)
	}
	return result
}

func (c *wireClient) readPreparedResult() wireResult {
	result, complete := c.readResultHeader()
	if complete {
		return result
	}
	columnCount := len(result.columns)
	for {
		row := readWirePacket(c.t, c.conn)
		if len(row) == 0 {
			return wireResult{err: "empty prepared row"}
		}
		if row[0] == 0xfe && len(row) < 9 {
			return result
		}
		if row[0] != 0 {
			return wireResult{err: fmt.Sprintf("not a binary row %x", row)}
		}
		nullBytes := (columnCount + 9) / 8
		if len(row) < 1+nullBytes {
			return wireResult{err: fmt.Sprintf("truncated binary row %x", row)}
		}
		offset := 1 + nullBytes
		values := make([]string, columnCount)
		for index, column := range result.metadata {
			if row[1+(index+2)/8]&(1<<((index+2)%8)) != 0 {
				continue
			}
			switch column.typ {
			case 0x03:
				if offset+4 > len(row) {
					return wireResult{err: "truncated integer binary row"}
				}
				values[index] = strconv.FormatInt(int64(int32(binary.LittleEndian.Uint32(row[offset:offset+4]))), 10)
				offset += 4
			case 0x08:
				if offset+8 > len(row) {
					return wireResult{err: "truncated integer binary row"}
				}
				if column.flags&0x20 != 0 {
					values[index] = strconv.FormatUint(binary.LittleEndian.Uint64(row[offset:offset+8]), 10)
				} else {
					values[index] = strconv.FormatInt(int64(binary.LittleEndian.Uint64(row[offset:offset+8])), 10)
				}
				offset += 8
			case 0x05:
				if offset+8 > len(row) {
					return wireResult{err: "truncated float binary row"}
				}
				values[index] = strconv.FormatFloat(math.Float64frombits(binary.LittleEndian.Uint64(row[offset:offset+8])), 'g', -1, 64)
				offset += 8
			default:
				value, next, valid := readLengthString(row, offset)
				if !valid {
					return wireResult{err: fmt.Sprintf("malformed binary row %x", row)}
				}
				values[index], offset = value, next
			}
		}
		result.rows = append(result.rows, values)
	}
}

func (c *wireClient) readResultHeader() (wireResult, bool) {
	payload := readWirePacket(c.t, c.conn)
	if len(payload) == 0 {
		return wireResult{err: "empty result"}, true
	}
	if payload[0] == 0xff {
		return wireResult{err: string(payload[4:])}, true
	}
	if payload[0] == 0x00 {
		affected, _, ok := readLengthInt(payload, 1)
		if !ok {
			return wireResult{err: fmt.Sprintf("malformed OK packet %x", payload)}, true
		}
		return wireResult{affected: uint64(affected)}, true
	}
	columnCount, _, ok := readLengthInt(payload, 0)
	if !ok {
		return wireResult{err: fmt.Sprintf("malformed column count %x", payload)}, true
	}
	result := wireResult{columns: make([]string, columnCount), metadata: make([]wireColumn, columnCount)}
	for index := range result.columns {
		definition := readWirePacket(c.t, c.conn)
		column, valid := parseColumnDefinition(definition)
		if !valid {
			return wireResult{err: fmt.Sprintf("malformed column definition %x", definition)}, true
		}
		result.columns[index], result.metadata[index] = column.name, column
	}
	_ = readWirePacket(c.t, c.conn)
	return result, false
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func readWirePacket(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	payload := []byte{}
	for {
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Fatal(err)
		}
		length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
		start := len(payload)
		payload = append(payload, make([]byte, length)...)
		if _, err := io.ReadFull(conn, payload[start:]); err != nil {
			t.Fatal(err)
		}
		if length < (1<<24)-1 {
			return payload
		}
	}
}

func writeWirePacket(t *testing.T, conn net.Conn, sequence byte, payload []byte) {
	t.Helper()
	for {
		length := len(payload)
		if length > (1<<24)-1 {
			length = (1 << 24) - 1
		}
		header := []byte{byte(length), byte(length >> 8), byte(length >> 16), sequence}
		if _, err := conn.Write(append(header, payload[:length]...)); err != nil {
			t.Fatal(err)
		}
		payload = payload[length:]
		if length < (1<<24)-1 {
			return
		}
		sequence++
	}
}

func readLengthInt(payload []byte, offset int) (int, int, bool) {
	if offset >= len(payload) {
		return 0, offset, false
	}
	prefix := payload[offset]
	offset++
	switch prefix {
	case 0xfc:
		if offset+2 > len(payload) {
			return 0, offset, false
		}
		return int(binary.LittleEndian.Uint16(payload[offset : offset+2])), offset + 2, true
	case 0xfd:
		if offset+3 > len(payload) {
			return 0, offset, false
		}
		return int(payload[offset]) | int(payload[offset+1])<<8 | int(payload[offset+2])<<16, offset + 3, true
	case 0xfe:
		if offset+8 > len(payload) {
			return 0, offset, false
		}
		return int(binary.LittleEndian.Uint64(payload[offset : offset+8])), offset + 8, true
	default:
		return int(prefix), offset, true
	}
}

func readLengthString(payload []byte, offset int) (string, int, bool) {
	if offset < len(payload) && payload[offset] == 0xfb {
		return "", offset + 1, true
	}
	length, next, ok := readLengthInt(payload, offset)
	if !ok || next+length > len(payload) {
		return "", next, false
	}
	return string(payload[next : next+length]), next + length, true
}

func bytesIndex(value []byte, target byte) int {
	for index, item := range value {
		if item == target {
			return index
		}
	}
	return -1
}

func TestMySQLStrictNumericAndBitSemantics(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("numeric-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "numeric-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE t (small TINYINT, count INT UNSIGNED, price DECIMAL(10,2), ratio DOUBLE, active BOOLEAN, mask BIT(8))",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	for _, rejected := range []string{
		"CREATE TABLE bad_decimal (v DECIMAL(66,2))",
		"CREATE TABLE bad_bit (v BIT(65))",
	} {
		if result := client.query(rejected); result.err == "" {
			t.Fatalf("out-of-ceiling declaration accepted: %q", rejected)
		}
	}

	inserted := client.query("INSERT INTO t VALUES (-128, 007, 1.5, 2.5, TRUE, b'101')")
	if inserted.err != "" || inserted.affected != 1 {
		t.Fatalf("valid numeric insert: %#v", inserted)
	}
	selected := client.query("SELECT small, count, price, ratio, active, mask FROM t")
	if selected.err != "" || len(selected.rows) != 1 || strings.Join(selected.rows[0], ",") != "-128,7,1.50,2.5,1,5" {
		t.Fatalf("canonical numeric values: %#v", selected)
	}
	if len(selected.metadata) != 6 || selected.metadata[2].typ != 0xf6 || selected.metadata[4].typ != 0x01 || selected.metadata[5].typ != 0x10 {
		t.Fatalf("numeric result metadata types: %#v", selected.metadata)
	}
	if selected.metadata[1].flags&0x20 == 0 {
		t.Fatalf("unsigned column missing unsigned flag: %#v", selected.metadata[1])
	}

	for _, rejected := range []string{
		"INSERT INTO t (small) VALUES (128)",
		"INSERT INTO t (count) VALUES (-1)",
		"INSERT INTO t (price) VALUES (1.234)",
		"INSERT INTO t (ratio) VALUES ('inf')",
		"INSERT INTO t (mask) VALUES (256)",
		"INSERT INTO t (small) VALUES ('abc')",
	} {
		if result := client.query(rejected); result.err == "" {
			t.Fatalf("strict numeric violation accepted: %q", rejected)
		}
	}
	if rows := client.query("SELECT small FROM t"); rows.err != "" || len(rows.rows) != 1 {
		t.Fatalf("rejected numeric writes leaked rows: %#v", rows)
	}

	if failed := client.query("UPDATE t SET small = 200 WHERE small = -128"); failed.err == "" {
		t.Fatalf("out-of-range update accepted: %#v", failed)
	}
	if rows := client.query("SELECT small FROM t"); rows.err != "" || len(rows.rows) != 1 || rows.rows[0][0] != "-128" {
		t.Fatalf("rejected update leaked a partial change: %#v", rows)
	}

	if explained := client.query("EXPLAIN SELECT small, mask FROM t WHERE count = 7"); explained.err != "" || len(explained.rows) == 0 {
		t.Fatalf("numeric query is not explainable: %#v", explained)
	}

	// A non-canonical predicate literal must compare against the canonical
	// stored value: WHERE count = 007 matches the stored 7.
	if matched := client.query("SELECT small FROM t WHERE count = 007"); matched.err != "" || len(matched.rows) != 1 || matched.rows[0][0] != "-128" {
		t.Fatalf("non-canonical numeric predicate did not match canonical value: %#v", matched)
	}
	if updated := client.query("UPDATE t SET small = 42 WHERE count = 007"); updated.err != "" || updated.affected != 1 {
		t.Fatalf("non-canonical predicate update: %#v", updated)
	}
}

func TestMySQLEnforcesCharacterCollationAndIdentifierSemantics(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("charset-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, "--format=json"); result.ExitCode != 0 {
		t.Fatalf("initialize: %#v", result)
	}
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "charset-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE people (tag VARCHAR(4), sensitive VARCHAR(8) COLLATE utf8mb4_bin)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	// utf8mb4 result metadata advertises the two supported collations.
	described := client.query("SELECT tag, sensitive FROM people")
	if described.err != "" || len(described.metadata) != 2 || described.metadata[0].characterSet != 255 || described.metadata[1].characterSet != 46 {
		t.Fatalf("collation metadata: %#v", described)
	}

	if inserted := client.query("INSERT INTO people (tag, sensitive) VALUES ('Ada', 'Ada')"); inserted.err != "" || inserted.affected != 1 {
		t.Fatalf("insert: %#v", inserted)
	}

	// utf8mb4_0900_ai_ci matches case-insensitively; utf8mb4_bin does not.
	if ci := client.query("SELECT tag FROM people WHERE tag = 'ADA'"); ci.err != "" || len(ci.rows) != 1 || ci.rows[0][0] != "Ada" {
		t.Fatalf("case-insensitive default collation: %#v", ci)
	}
	if bin := client.query("SELECT sensitive FROM people WHERE sensitive = 'ADA'"); bin.err != "" || len(bin.rows) != 0 {
		t.Fatalf("utf8mb4_bin must be case-sensitive: %#v", bin)
	}
	if exact := client.query("SELECT sensitive FROM people WHERE sensitive = 'Ada'"); exact.err != "" || len(exact.rows) != 1 {
		t.Fatalf("utf8mb4_bin exact match: %#v", exact)
	}

	// An assignment past the declared length fails and leaves no durable effect.
	if tooLong := client.query("INSERT INTO people (tag) VALUES ('toolong')"); tooLong.err == "" {
		t.Fatalf("over-length assignment accepted: %#v", tooLong)
	}
	if after := client.query("SELECT tag FROM people"); after.err != "" || len(after.rows) != 1 {
		t.Fatalf("rejected write changed table: %#v", after)
	}

	// An unsupported collation fails before any durable table exists.
	if bad := client.query("CREATE TABLE bad (c VARCHAR(4) COLLATE utf8mb4_general_ci)"); bad.err == "" {
		t.Fatalf("unsupported collation accepted: %#v", bad)
	}
	if reused := client.query("CREATE TABLE bad (c VARCHAR(4))"); reused.err != "" {
		t.Fatalf("rejected DDL left durable table: %#v", reused)
	}

	// Identifiers preserve spelling but collide under canonical caseless matching.
	if create := client.query("CREATE TABLE Ledger (id INT)"); create.err != "" {
		t.Fatalf("mixed-case table: %#v", create)
	}
	if insert := client.query("INSERT INTO ledger (id) VALUES (7)"); insert.err != "" || insert.affected != 1 {
		t.Fatalf("caseless table reference: %#v", insert)
	}
	if selected := client.query("SELECT id FROM LEDGER"); selected.err != "" || len(selected.rows) != 1 || selected.rows[0][0] != "7" {
		t.Fatalf("caseless table lookup: %#v", selected)
	}
	shown := client.query("SHOW CREATE TABLE ledger")
	if shown.err != "" || len(shown.rows) != 1 || shown.rows[0][0] != "Ledger" {
		t.Fatalf("declared spelling not preserved: %#v", shown)
	}

	// An identifier past the fixed 64-scalar ceiling fails explicitly.
	overLong := strings.Repeat("n", 65)
	if bad := client.query("CREATE TABLE " + overLong + " (id INT)"); bad.err == "" {
		t.Fatalf("over-length identifier accepted: %#v", bad)
	}
}
