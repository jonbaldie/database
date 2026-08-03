package blackbox_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestOperatorDataValidateReportsHealthyStoppedInstance(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "data-validate-secret")

	validated := runner.Run(context.Background(),
		"data", "validate",
		"--data-directory", directory,
		"--result=json",
	)
	if validated.ExitCode != 0 {
		t.Fatalf("data validate: %#v", validated)
	}
	result := decodeOperatorResult(t, validated.Stdout)
	if result["exit_class"] != "success" || result["command"] != "data validate" {
		t.Fatalf("data validate result = %#v", result)
	}
	if valid, _ := result["valid"].(bool); !valid {
		t.Fatalf("data validate valid = %#v", result)
	}
}

func TestOperatorDataValidateFailsClosedWithoutRepair(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "data-validate-corrupt")
	catalogPath := filepath.Join(directory, "catalog.json")
	before, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, []byte(`{"namespaces":{"broken":null}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	failed := runner.Run(context.Background(),
		"data", "validate",
		"--data-directory", directory,
		"--result=json",
	)
	if failed.ExitCode != 5 {
		t.Fatalf("corrupt validate exit = %d, want 5; %#v", failed.ExitCode, failed)
	}
	result := decodeOperatorResult(t, failed.Stdout)
	if result["exit_class"] != "invalid_artifact" {
		t.Fatalf("corrupt validate result = %#v", result)
	}
	after, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != `{"namespaces":{"broken":null}}` {
		t.Fatalf("data validate repaired catalog: before=%q after=%q", before, after)
	}
}

func TestOperatorDataInspectDoesNotValidateOrRepair(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "data-inspect-secret")
	artifact := filepath.Join(directory, ".catalog-crash.tmp")
	if err := os.WriteFile(artifact, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	inspected := runner.Run(context.Background(),
		"data", "inspect",
		"--data-directory", directory,
		"--result=json",
	)
	if inspected.ExitCode != 0 {
		t.Fatalf("data inspect: %#v", inspected)
	}
	result := decodeOperatorResult(t, inspected.Stdout)
	if result["exit_class"] != "success" {
		t.Fatalf("data inspect result = %#v", result)
	}
	if result["validated"] != false || result["integrity"] != "not-validated" || result["recovery_required"] != true {
		t.Fatalf("data inspect details = %#v", result)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("data inspect changed recovery artifact: %v", err)
	}
}

func TestOperatorConfigValidateReportsFlagOverEnvironmentPrecedence(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "config-precedence-secret")
	configFile := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(configFile, []byte("max_connections = 10\nlog_format = \"text\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(executable,
		"config", "validate",
		"--format=json",
		"--config", configFile,
		"--data-directory="+directory,
		"--max-connections=12",
	)
	command.Env = append(os.Environ(),
		"DATABASE_SERVER_MAX_CONNECTIONS=11",
		"DATABASE_SERVER_LOG_FORMAT=json",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("config validate precedence: %v output=%s", err, output)
	}
	var result map[string]any
	if decodeErr := json.Unmarshal(output, &result); decodeErr != nil {
		t.Fatalf("decode precedence result: %v output=%s", decodeErr, output)
	}
	settings, _ := result["settings"].(map[string]any)
	maxConnections, _ := settings["max_connections"].(map[string]any)
	logFormat, _ := settings["log_format"].(map[string]any)
	if maxConnections["value"] != "12" || maxConnections["source"] != "flag" {
		t.Fatalf("max_connections = %#v", maxConnections)
	}
	if logFormat["value"] != "json" || logFormat["source"] != "environment" {
		t.Fatalf("log_format = %#v", logFormat)
	}
}

func TestMySQLTableLifecycleSupportsRenameTruncateAndDrop(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "ddl-lifecycle-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	client := newWireClient(t, address, "admin", password)
	defer client.close()
	mustQuery(t, client, "CREATE DATABASE app")
	mustQuery(t, client, "USE app")
	mustQuery(t, client, "CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(20) NOT NULL)")
	mustQuery(t, client, "INSERT INTO items VALUES (1, 'a'), (2, 'b')")
	mustQuery(t, client, "ALTER TABLE items ADD COLUMN note VARCHAR(10) NULL")
	mustQuery(t, client, "UPDATE items SET note = 'ok' WHERE id = 1")
	altered := client.query("SELECT id, name, note FROM items WHERE id = 1")
	if altered.err != "" || len(altered.rows) != 1 || altered.rows[0][2] != "ok" {
		t.Fatalf("alter result = %#v", altered)
	}
	mustQuery(t, client, "RENAME TABLE items TO goods")
	mustQuery(t, client, "TRUNCATE TABLE goods")
	truncated := client.query("SELECT COUNT(*) FROM goods")
	if truncated.err != "" || len(truncated.rows) != 1 || truncated.rows[0][0] != "0" {
		t.Fatalf("truncate result = %#v", truncated)
	}
	mustQuery(t, client, "DROP TABLE goods")
	missing := client.query("SELECT * FROM goods")
	if missing.errCode != 1146 {
		t.Fatalf("drop table error = %#v", missing)
	}
	mustQuery(t, client, "DROP DATABASE app")
}

func TestOperatorConfigValidateAcceptsDefaultsAndRejectsUnknown(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "config-validate-secret")

	accepted := runner.Run(context.Background(),
		"config", "validate",
		"--format=json",
		"--data-directory="+directory,
	)
	if accepted.ExitCode != 0 {
		t.Fatalf("config validate: %#v", accepted)
	}
	var acceptedResult map[string]any
	if err := json.Unmarshal([]byte(accepted.Stdout), &acceptedResult); err != nil {
		t.Fatalf("decode config validate: %v", err)
	}
	if acceptedResult["schema"] != "database.configuration/v1" || acceptedResult["exit_class"] != "success" || acceptedResult["operation_id"] == "" {
		t.Fatalf("config validate result = %#v", acceptedResult)
	}
	if _, ok := acceptedResult["settings"].(map[string]any); !ok {
		t.Fatalf("config validate settings missing: %#v", acceptedResult)
	}

	rejected := runner.Run(context.Background(),
		"config", "validate",
		"--format=json",
		"--unknown-setting=value",
	)
	if rejected.ExitCode != 2 {
		t.Fatalf("unknown config exit = %d, want 2; %#v", rejected.ExitCode, rejected)
	}
	var rejectedResult map[string]any
	if err := json.Unmarshal([]byte(rejected.Stdout), &rejectedResult); err != nil {
		t.Fatalf("decode unknown config: %v", err)
	}
	if rejectedResult["exit_class"] != "invalid_input" {
		t.Fatalf("unknown config result = %#v", rejectedResult)
	}
}

func TestOperatorBackupInspectAndRestoreRejectNonEmptyDestination(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "backup-inspect-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)

	archive := filepath.Join(t.TempDir(), "instance.tar")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := runner.Run(context.Background(),
		"backup", "create",
		"--address="+address,
		"--account=admin",
		"--password-file", passwordFile,
		"--output", archive,
		"--result=json",
	)
	if created.ExitCode != 0 {
		t.Fatalf("backup create: %#v", created)
	}
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("stop after backup: %#v", result)
	}

	inspected := runner.Run(context.Background(), "backup", "inspect", "--backup", archive, "--result=json")
	if inspected.ExitCode != 0 {
		t.Fatalf("backup inspect: %#v", inspected)
	}
	inspectResult := decodeOperatorResult(t, inspected.Stdout)
	if inspectResult["exit_class"] != "success" {
		t.Fatalf("backup inspect result = %#v", inspectResult)
	}

	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "existing.txt"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := runner.Run(context.Background(),
		"restore",
		"--backup", archive,
		"--data-directory", occupied,
		"--result=json",
	)
	if denied.ExitCode != 6 {
		t.Fatalf("restore non-empty exit = %d, want 6; %#v", denied.ExitCode, denied)
	}
	deniedResult := decodeOperatorResult(t, denied.Stdout)
	if deniedResult["exit_class"] != "operation_failed" {
		t.Fatalf("restore non-empty result = %#v", deniedResult)
	}
}

func TestOperatorUpgradeUsesMatchingOnlineBackup(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "upgrade-online-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)

	client := newWireClient(t, address, "admin", password)
	mustQuery(t, client, "CREATE DATABASE upgrade_data")
	mustQuery(t, client, "USE upgrade_data")
	mustQuery(t, client, "CREATE TABLE records (id INT PRIMARY KEY)")
	mustQuery(t, client, "INSERT INTO records VALUES (7)")
	client.close()

	archive := filepath.Join(t.TempDir(), "pre-upgrade.tar")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	created := runner.Run(context.Background(),
		"backup", "create",
		"--address="+address,
		"--account=admin",
		"--password-file", passwordFile,
		"--output", archive,
		"--result=json",
	)
	if created.ExitCode != 0 {
		t.Fatalf("pre-upgrade backup: %#v", created)
	}

	stopped := runner.Run(context.Background(),
		"shutdown",
		"--yes",
		"--address="+address,
		"--account=admin",
		"--password-file", passwordFile,
		"--result=json",
	)
	if stopped.ExitCode != 0 {
		t.Fatalf("shutdown before upgrade: %#v", stopped)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("serve after shutdown: %#v", result)
	}

	upgraded := runner.Run(context.Background(),
		"upgrade",
		"--data-directory", directory,
		"--backup", archive,
		"--target-version", "0.1.1",
		"--yes",
		"--result=json",
	)
	if upgraded.ExitCode != 0 {
		t.Fatalf("upgrade: %#v", upgraded)
	}
	upgradeResult := decodeOperatorResult(t, upgraded.Stdout)
	if upgradeResult["exit_class"] != "success" {
		t.Fatalf("upgrade result = %#v", upgradeResult)
	}

	metadata, err := os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metadata), `"data_version": "0.1.1"`) && !strings.Contains(string(metadata), `"data_version":"0.1.1"`) {
		t.Fatalf("upgraded metadata missing 0.1.1: %s", metadata)
	}

	withoutYes := runner.Run(context.Background(),
		"upgrade",
		"--data-directory", directory,
		"--backup", archive,
		"--target-version", "0.1.2",
		"--result=json",
	)
	if withoutYes.ExitCode != 2 {
		t.Fatalf("upgrade without --yes exit = %d, want 2; %#v", withoutYes.ExitCode, withoutYes)
	}
}

func decodeOperatorResult(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode operator result: %v stdout=%q", err, stdout)
	}
	if result["schema"] != "database.operator.result/v1" || result["operation_id"] == "" {
		t.Fatalf("operator result missing identity: %#v", result)
	}
	return result
}
