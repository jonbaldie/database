package blackbox_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestOperatorDataResultsHideStorageLayoutAndReportCheckTime(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "data-contract-secret")

	t.Run("stopped", func(t *testing.T) {
		assertOperatorDataResultsHideStorageLayout(t, runner, directory)
	})

	process, _ := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	t.Run("serving", func(t *testing.T) {
		assertOperatorDataResultsHideStorageLayout(t, runner, directory)
	})
}

func assertOperatorDataResultsHideStorageLayout(t *testing.T, runner blackbox.Runner, directory string) {
	t.Helper()
	for _, command := range []string{"inspect", "validate"} {
		run := runner.Run(context.Background(), "data", command,
			"--data-directory", directory, "--result=json", "--progress=none")
		if run.ExitCode != 0 {
			t.Fatalf("data %s: %#v", command, run)
		}
		fullOutput := run.Stdout + run.Stderr
		for _, internalName := range []string{
			".database-state", ".running.lock", "catalog.json", "instance.json",
			"rows/", "wal.log", "checkpoint.dat", "tables.meta",
		} {
			if strings.Contains(fullOutput, internalName) {
				t.Errorf("data %s exposed internal name %q: %s", command, internalName, fullOutput)
			}
		}
		result := decodeOperatorResult(t, run.Stdout)
		details, ok := result["details"].(map[string]any)
		if !ok {
			t.Fatalf("data %s details are not structured: %#v", command, result["details"])
		}
		if command == "inspect" {
			for _, key := range []string{"entries", "examined"} {
				if _, exists := details[key]; exists {
					t.Errorf("data inspect includes internal-layout field %q: %#v", key, details[key])
				}
			}
			for _, key := range []string{"instance_id", "data_version", "compatibility", "state", "recovery_required", "upgrade_required"} {
				if _, exists := details[key]; !exists {
					t.Errorf("data inspect lacks required fact %q: %#v", key, details)
				}
			}
			continue
		}

		if details["instance_id"] == "" || details["data_directory"] != directory {
			t.Errorf("data validate identity or directory facts = %#v", details)
		}
		checkedAt, ok := details["checked_at"].(string)
		if !ok || checkedAt == "" {
			t.Fatalf("data validate check time = %#v", details["checked_at"])
		}
		checkedTime, err := time.Parse(time.RFC3339Nano, checkedAt)
		if err != nil {
			t.Fatalf("data validate checked_at %q is not RFC 3339: %v", checkedAt, err)
		}
		_, offset := checkedTime.Zone()
		if offset != 0 {
			t.Errorf("data validate checked_at %q is not UTC", checkedAt)
		}
		if details["valid"] != true {
			t.Errorf("data validate valid = %#v", details["valid"])
		}
		if _, ok := details["findings"].([]any); !ok {
			t.Errorf("data validate findings are not structured: %#v", details["findings"])
		}
		examined, ok := details["examined"].([]any)
		if !ok {
			t.Errorf("data validate examined components are not structured: %#v", details["examined"])
			continue
		}
		allowedComponents := map[string]bool{
			"catalog": true, "data_directory": true, "initialization": true,
			"instance_metadata": true, "row_store": true, "upgrade_state": true,
		}
		for _, value := range examined {
			component, ok := value.(string)
			if !ok || !allowedComponents[component] {
				t.Errorf("data validate exposed non-logical examined component %#v", value)
			}
		}
	}
}

func TestOperatorDataValidateReportsUnreadableRowStoreAsLogicalComponent(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "data-row-store-secret")
	process, _ := startMySQLServer(t, runner, directory)
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("stop server before row-store damage: %#v", result)
	}
	walPath := filepath.Join(directory, "rows", "wal.log")
	if err := os.Chmod(walPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(walPath, 0o600) })

	failed := runner.Run(context.Background(), "data", "validate",
		"--data-directory", directory, "--result=json", "--progress=none")
	if failed.ExitCode != 5 {
		t.Fatalf("unreadable row-store validation exit = %d, want 5; %#v", failed.ExitCode, failed)
	}
	result := decodeOperatorResult(t, failed.Stdout)
	if result["exit_class"] != "invalid_artifact" || result["valid"] != false {
		t.Fatalf("unreadable row-store result = %#v", result)
	}
	findings, ok := result["findings"].([]any)
	if !ok {
		t.Fatalf("findings are not structured: %#v", result["findings"])
	}
	found := false
	for _, raw := range findings {
		finding, ok := raw.(map[string]any)
		if !ok || finding["code"] != "unreadable_entry" {
			continue
		}
		found = true
		if finding["component"] != "row_store" {
			t.Errorf("row-store finding component = %#v", finding["component"])
		}
		if _, exists := finding["path"]; exists {
			t.Errorf("row-store finding exposes a path: %#v", finding)
		}
	}
	if !found {
		t.Fatalf("missing unreadable row-store finding: %#v", findings)
	}
	fullOutput := failed.Stdout + failed.Stderr
	for _, internalName := range []string{"rows/", "wal.log", "catalog.json", "instance.json"} {
		if strings.Contains(fullOutput, internalName) {
			t.Errorf("data validate exposed internal name %q: %s", internalName, fullOutput)
		}
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
	if acceptedResult["schema"] != "database.operator.result/v1" || acceptedResult["record_type"] != "result" || acceptedResult["command"] != "config validate" || acceptedResult["exit_class"] != "success" || acceptedResult["status"] != "success" || acceptedResult["operation_id"] == "" {
		t.Fatalf("config validate result = %#v", acceptedResult)
	}
	if details, ok := acceptedResult["details"].(map[string]any); !ok || details["settings"] == nil {
		t.Fatalf("config validate details missing settings: %#v", acceptedResult["details"])
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
	if denied.ExitCode != 3 {
		t.Fatalf("restore non-empty exit = %d, want 3; %#v", denied.ExitCode, denied)
	}
	deniedResult := decodeOperatorResult(t, denied.Stdout)
	if deniedResult["exit_class"] != "precondition" {
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

func TestOperatorResultFormatsShareOneEnvelope(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	options := []struct {
		name string
		flag string
	}{
		{name: "result", flag: "--result=json"},
		{name: "format", flag: "--format=json"},
	}

	t.Run("initialization", func(t *testing.T) {
		password := "operator-result-init-secret"
		passwordFile := filepath.Join(t.TempDir(), "password")
		if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		results := make([]map[string]any, 0, len(options))
		for _, option := range options {
			directory := filepath.Join(t.TempDir(), "instance")
			run := runner.Run(context.Background(), "init", directory, "--password-file", passwordFile, option.flag)
			if run.ExitCode != 0 {
				t.Fatalf("init %s: %#v", option.name, run)
			}
			result := decodeOperatorResult(t, run.Stdout)
			assertOperatorResultEnvelope(t, result, "init", "success")
			results = append(results, result)
		}
		assertSameOperatorResultShape(t, results[0], results[1])
	})

	t.Run("configuration failure", func(t *testing.T) {
		results := make([]map[string]any, 0, len(options))
		for _, option := range options {
			run := runner.Run(context.Background(), "config", "validate", "--unknown-setting=value", option.flag)
			if run.ExitCode != 2 {
				t.Fatalf("config validate %s exit = %d, want 2; %#v", option.name, run.ExitCode, run)
			}
			result := decodeOperatorResult(t, run.Stdout)
			assertOperatorResultEnvelope(t, result, "config validate", "failure")
			results = append(results, result)
		}
		assertSameOperatorResultShape(t, results[0], results[1])
	})

	t.Run("serve", func(t *testing.T) {
		password := "operator-result-serve-secret"
		passwordFile := filepath.Join(t.TempDir(), "password")
		if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		results := make([]map[string]any, 0, len(options))
		for _, option := range options {
			directory := filepath.Join(t.TempDir(), "instance")
			initializeServer(t, runner, directory, password)
			mysqlAddress := freeAddress(t)
			diagnosticsAddress := freeAddress(t)
			process, err := runner.Start(context.Background(), "serve",
				"--data-directory", directory,
				"--mysql-listen-address", mysqlAddress,
				"--diagnostics-listen-address", diagnosticsAddress,
				option.flag,
			)
			if err != nil {
				t.Fatal(err)
			}
			processFinished := false
			t.Cleanup(func() {
				if !processFinished {
					_ = process.Stop()
					_ = process.Wait()
				}
			})
			waitForDiagnosticsReady(t, diagnosticsAddress)
			stopped := runner.Run(context.Background(), "shutdown", "--yes", "--address="+mysqlAddress,
				"--account=admin", "--password-file", passwordFile, "--result=json")
			if stopped.ExitCode != 0 {
				_ = process.Stop()
				t.Fatalf("shutdown after serve %s: %#v", option.name, stopped)
			}
			served := process.Wait()
			processFinished = true
			if served.ExitCode != 0 {
				t.Fatalf("serve %s exit = %d; stdout=%s stderr=%s", option.name, served.ExitCode, served.Stdout, served.Stderr)
			}
			result := decodeLastOperatorResult(t, served.Stdout)
			assertOperatorResultEnvelope(t, result, "serve", "success")
			results = append(results, result)
		}
		assertSameOperatorResultShape(t, results[0], results[1])
	})

	t.Run("data validation", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "instance")
		initializeServer(t, runner, directory, "operator-result-data-secret")
		results := make([]map[string]any, 0, len(options))
		for _, option := range options {
			run := runner.Run(context.Background(), "data", "validate", "--data-directory", directory, option.flag)
			if run.ExitCode != 0 {
				t.Fatalf("data validate %s: %#v", option.name, run)
			}
			result := decodeOperatorResult(t, run.Stdout)
			assertOperatorResultEnvelope(t, result, "data validate", "success")
			results = append(results, result)
		}
		assertSameOperatorResultShape(t, results[0], results[1])
	})

	t.Run("data inspection", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "instance")
		initializeServer(t, runner, directory, "operator-result-inspect-secret")
		results := make([]map[string]any, 0, len(options))
		for _, option := range options {
			run := runner.Run(context.Background(), "data", "inspect", "--data-directory", directory, option.flag)
			if run.ExitCode != 0 {
				t.Fatalf("data inspect %s: %#v", option.name, run)
			}
			result := decodeOperatorResult(t, run.Stdout)
			assertOperatorResultEnvelope(t, result, "data inspect", "success")
			results = append(results, result)
		}
		assertSameOperatorResultShape(t, results[0], results[1])
	})
}

func TestOperatorServeKeepsDefaultHumanOutput(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	password := "operator-human-serve-secret"
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, password)
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mysqlAddress := freeAddress(t)
	diagnosticsAddress := freeAddress(t)
	process, err := runner.Start(context.Background(), "serve",
		"--data-directory", directory,
		"--mysql-listen-address", mysqlAddress,
		"--diagnostics-listen-address", diagnosticsAddress,
	)
	if err != nil {
		t.Fatal(err)
	}
	processFinished := false
	t.Cleanup(func() {
		if !processFinished {
			_ = process.Stop()
			_ = process.Wait()
		}
	})
	waitForDiagnosticsReady(t, diagnosticsAddress)
	stopped := runner.Run(context.Background(), "shutdown", "--yes", "--address="+mysqlAddress,
		"--account=admin", "--password-file", passwordFile, "--result=json")
	if stopped.ExitCode != 0 {
		t.Fatalf("shutdown after human serve: %#v", stopped)
	}
	served := process.Wait()
	processFinished = true
	if served.ExitCode != 0 {
		t.Fatalf("human serve exit = %d; stdout=%s stderr=%s", served.ExitCode, served.Stdout, served.Stderr)
	}
	if !strings.Contains(served.Stdout, "database: ready (diagnostics=") || !strings.Contains(served.Stdout, "database serve: success (operation_id=op-") || served.Stderr != "" {
		t.Fatalf("human serve output changed: stdout=%q stderr=%q", served.Stdout, served.Stderr)
	}
}

func TestOperatorServeReportsUnusableDataDirectoryAsPrecondition(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	empty := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	for _, directory := range []string{empty, missing} {
		for _, flag := range []string{"", "--format=json", "--result=json"} {
			arguments := []string{"serve", "--data-directory", directory, "--mysql-listen-address", freeAddress(t)}
			if flag != "" {
				arguments = append(arguments, flag)
			}
			run := runner.Run(context.Background(), arguments...)
			if run.ExitCode != 3 {
				t.Fatalf("serve %s %s exit = %d, want 3; %#v", directory, flag, run.ExitCode, run)
			}
			if flag == "" {
				if !strings.Contains(run.Stderr, "database serve: ") {
					t.Fatalf("serve %s human failure output: %#v", directory, run)
				}
				continue
			}
			result := decodeOperatorResult(t, run.Stdout)
			assertOperatorResultEnvelope(t, result, "serve", "failure")
			if result["exit_class"] != "precondition" || result["exit_code"] != float64(3) {
				t.Fatalf("serve %s %s result = %#v, want precondition/3", directory, flag, result)
			}
		}
	}
}

func assertOperatorResultEnvelope(t *testing.T, result map[string]any, command, status string) {
	t.Helper()
	for _, field := range []string{
		"schema", "record_type", "operation_id", "command", "status", "exit_class", "exit_code",
		"started_at", "finished_at", "duration_ms", "details", "diagnostics",
	} {
		if _, ok := result[field]; !ok {
			t.Errorf("%s result lacks required field %q: %#v", command, field, result)
		}
	}
	if result["command"] != command {
		t.Errorf("result command = %#v, want %q", result["command"], command)
	}
	if result["status"] != status {
		t.Errorf("result status = %#v, want %q", result["status"], status)
	}
}

func assertSameOperatorResultShape(t *testing.T, first, second map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(operatorResultShape(first), operatorResultShape(second)) {
		t.Errorf("result shapes differ:\n first: %#v\nsecond: %#v", operatorResultShape(first), operatorResultShape(second))
	}
}

func operatorResultShape(value any) any {
	switch value := value.(type) {
	case map[string]any:
		shape := make(map[string]any, len(value))
		for key, child := range value {
			shape[key] = operatorResultShape(child)
		}
		return shape
	case []any:
		elementShapes := make([]any, 0, len(value))
		for _, child := range value {
			childShape := operatorResultShape(child)
			seen := false
			for _, existing := range elementShapes {
				if reflect.DeepEqual(existing, childShape) {
					seen = true
					break
				}
			}
			if !seen {
				elementShapes = append(elementShapes, childShape)
			}
		}
		return map[string]any{"array_elements": elementShapes}
	case nil:
		return nil
	default:
		return reflect.TypeOf(value).String()
	}
}

func decodeLastOperatorResult(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var result map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		var candidate map[string]any
		if err := json.Unmarshal([]byte(line), &candidate); err == nil && candidate["schema"] == "database.operator.result/v1" && candidate["record_type"] == "result" {
			result = candidate
		}
	}
	if result == nil {
		t.Fatalf("operator result not found in stdout %q", stdout)
	}
	return result
}

func waitForDiagnosticsReady(t *testing.T, address string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		var response map[string]any
		status, err := blackbox.HTTPJSON(ctx, address, "/ready", &response)
		if err == nil && status == 200 {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("diagnostics listener %s did not become ready", address)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
