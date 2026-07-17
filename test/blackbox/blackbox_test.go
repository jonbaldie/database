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
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	state := filepath.Join(t.TempDir(), "state", "server.state")
	process, address, _ := startServer(t, runner, state)
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
	if contents, _ := os.ReadFile(state); string(contents) != "stopped\n" {
		t.Fatalf("state after graceful stop = %q", contents)
	}

	process, _, _ = startServer(t, runner, state)
	if err := process.Crash(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.Err == nil {
		t.Fatalf("crash should be visible: %#v", result)
	}
	process, _, recovered := startServer(t, runner, state)
	if !recovered {
		t.Fatal("restart did not expose the prior unclean stop")
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("final stop: %#v", result)
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

func startServer(t *testing.T, runner blackbox.Runner, state string) (*blackbox.Process, string, bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	process, err := runner.Start(context.Background(), "serve", "--format=json", "--diagnostics-address="+address, "--state-file="+state)
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
	return process, address, event.Recovered
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
	state := filepath.Join(t.TempDir(), "server.state")
	process, err := runner.Start(context.Background(), "serve", "--data-dir", directory, "--mysql-address", mysql, "--diagnostics-address", diagnostics, "--format=json", "--state-file", state)
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
	if got := client.query("USE rolled_back"); got.err == "" {
		t.Fatalf("rolled back namespace remained: %#v", got)
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
	process, err = runner.Start(context.Background(), "serve", "--data-dir", directory, "--mysql-address", mysql, "--format=json", "--state-file", state)
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
	process, err := runner.Start(context.Background(), "serve", "--data-dir", directory, "--mysql-address", address, "--tls-cert", certificate, "--tls-key", key, "--format=json", "--state-file", filepath.Join(t.TempDir(), "state"))
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
	if !ok || column.catalog != "def" || column.schema != "" || column.table != "" || column.originalTable != "" || column.name != "'Ada'" || column.originalName != "" || column.characterSet != 45 || column.length != 12 || column.typ != 0xfd || column.flags != 1 || column.decimals != 0 {
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
	for _, row := range metadata.rows {
		if row[0] == "pending" {
			t.Fatalf("uncommitted table leaked into metadata: %#v", metadata)
		}
	}
	if result := client.query("COMMIT"); result.err != "" {
		t.Fatalf("commit: %#v", result)
	}
	metadata = client.query("SELECT TABLE_NAME FROM information_schema.tables")
	found := false
	for _, row := range metadata.rows {
		if row[0] == "pending" {
			found = true
		}
	}
	if !found {
		t.Fatalf("committed table missing from metadata: %#v", metadata)
	}
}

func startMySQLServer(t *testing.T, runner blackbox.Runner, directory string) (*blackbox.Process, string) {
	t.Helper()
	diagnostics := freeAddress(t)
	mysqlAddress := freeAddress(t)
	state := filepath.Join(t.TempDir(), "server.state")
	process, err := runner.Start(context.Background(), "serve", "--data-dir", directory, "--mysql-address", mysqlAddress, "--diagnostics-address", diagnostics, "--format=json", "--state-file", state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var event map[string]any
	if err := process.NextJSONEvent(ctx, &event); err != nil || event["state"] != "ready" {
		process.Crash()
		result := process.Wait()
		t.Fatalf("wait for ready event: %v; result=%#v", err, result)
	}
	return process, mysqlAddress
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
	second := runner.Run(context.Background(), "serve", "--data-dir", directory, "--mysql-address", freeAddress(t), "--format=json")
	if second.ExitCode != 1 || !strings.Contains(second.Stdout, "already in use") {
		t.Fatalf("second owner: %#v", second)
	}

	client := newWireClient(t, address, "admin", "shutdown-secret")
	if result := client.query("BEGIN"); result.err != "" {
		t.Fatalf("begin: %#v", result)
	}
	if result := client.query("CREATE DATABASE interrupted"); result.err != "" {
		t.Fatalf("create uncommitted database: %#v", result)
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
	if result := client.query("USE interrupted"); result.err == "" {
		t.Fatalf("uncommitted database survived shutdown: %#v", result)
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
	if result := runner.Run(context.Background(), "serve", "--data-dir", damaged, "--mysql-address", freeAddress(t), "--format=json"); result.ExitCode != 1 || !strings.Contains(result.Stdout, "catalog") {
		t.Fatalf("damaged directory: %#v", result)
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
	columns []string
	rows    [][]string
	err     string
}

type preparedStatement struct {
	id  uint32
	err string
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
	if len(payload) < 5 || payload[0] != 0 {
		return preparedStatement{err: fmt.Sprintf("prepare response %x", payload)}
	}
	return preparedStatement{id: binary.LittleEndian.Uint32(payload[1:5])}
}

func (c *wireClient) executePrepared(id uint32) wireResult {
	payload := []byte{0x17, byte(id), byte(id >> 8), byte(id >> 16), byte(id >> 24), 0, 0, 0, 0, 0}
	writeWirePacket(c.t, c.conn, 0, payload)
	return c.readResult()
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
	payload := readWirePacket(c.t, c.conn)
	if len(payload) == 0 {
		return wireResult{err: "empty result"}
	}
	if payload[0] == 0xff {
		return wireResult{err: string(payload[4:])}
	}
	if payload[0] == 0x00 {
		return wireResult{}
	}
	columnCount, _, ok := readLengthInt(payload, 0)
	if !ok {
		return wireResult{err: fmt.Sprintf("malformed column count %x", payload)}
	}
	result := wireResult{columns: make([]string, columnCount)}
	for i := range result.columns {
		definition := readWirePacket(c.t, c.conn)
		name, ok := readColumnName(definition)
		if !ok {
			return wireResult{err: fmt.Sprintf("malformed column definition %x", definition)}
		}
		result.columns[i] = name
	}
	_ = readWirePacket(c.t, c.conn)
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
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, int(header[0])|int(header[1])<<8|int(header[2])<<16)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeWirePacket(t *testing.T, conn net.Conn, sequence byte, payload []byte) {
	t.Helper()
	header := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), sequence}
	if _, err := conn.Write(append(header, payload...)); err != nil {
		t.Fatal(err)
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

func readColumnName(payload []byte) (string, bool) {
	offset := 0
	for i := 0; i < 4; i++ {
		_, next, ok := readLengthString(payload, offset)
		if !ok {
			return "", false
		}
		offset = next
	}
	name, _, ok := readLengthString(payload, offset)
	return name, ok
}

func bytesIndex(value []byte, target byte) int {
	for index, item := range value {
		if item == target {
			return index
		}
	}
	return -1
}
