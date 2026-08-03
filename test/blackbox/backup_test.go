package blackbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestOnlineBackupCreateCapturesCommittedStateWhileServerRuns(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "online-backup-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	client := newWireClient(t, address, "admin", password)
	defer client.close()
	mustQuery(t, client, "CREATE DATABASE backup_data")
	mustQuery(t, client, "USE backup_data")
	mustQuery(t, client, "CREATE TABLE records (id INT)")
	mustQuery(t, client, "INSERT INTO records VALUES (1)")
	mustQuery(t, client, "BEGIN")
	mustQuery(t, client, "INSERT INTO records VALUES (2)")

	archive := filepath.Join(t.TempDir(), "online.tar")
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
		"--progress=json",
	)
	if created.ExitCode != 0 {
		t.Fatalf("online backup create: %#v", created)
	}
	assertOnlineBackupProgress(t, created.Stderr)
	var createResult map[string]any
	if err := json.Unmarshal([]byte(created.Stdout), &createResult); err != nil {
		t.Fatalf("decode create result: %v stdout=%q", err, created.Stdout)
	}
	if createResult["exit_class"] != "success" || createResult["complete"] != true {
		t.Fatalf("create result = %#v", createResult)
	}
	if createResult["source_instance_id"] == "" || createResult["artifact_path"] == "" {
		t.Fatalf("create details missing identity: %#v", createResult)
	}
	if createResult["backup_version"] == "" || createResult["created_at"] == "" || createResult["size_bytes"] == nil {
		t.Fatalf("create details incomplete: %#v", createResult)
	}

	mustQuery(t, client, "COMMIT")
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if result := process.Wait(); result.ExitCode != 0 {
		t.Fatalf("stop source server: %#v", result)
	}

	destination := filepath.Join(t.TempDir(), "restored")
	restored := runner.Run(context.Background(), "restore", "--backup", archive, "--data-directory", destination, "--result=json")
	if restored.ExitCode != 0 {
		t.Fatalf("restore online backup: %#v", restored)
	}

	restoredProcess, restoredAddress := startMySQLServer(t, runner, destination)
	defer func() { _ = restoredProcess.Stop(); _ = restoredProcess.Wait() }()
	restoredClient := newWireClient(t, restoredAddress, "admin", password)
	defer restoredClient.close()
	mustQuery(t, restoredClient, "USE backup_data")
	result := restoredClient.query("SELECT id FROM records ORDER BY id")
	if result.err != "" || len(result.rows) != 1 || result.rows[0][0] != "1" {
		t.Fatalf("restored committed rows = %#v", result)
	}
}

func TestOnlineBackupCreateRejectsMissingOperationalControl(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "online-backup-denied"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", password)
	defer admin.close()
	mustQuery(t, admin, "CREATE USER 'reader' IDENTIFIED BY 'reader-backup-secret'")
	mustQuery(t, admin, "GRANT OPERATIONAL_OBSERVATION ON *.* TO 'reader'")

	archive := filepath.Join(t.TempDir(), "denied.tar")
	denied := runner.RunWithStdin(context.Background(), "reader-backup-secret\n",
		"backup", "create",
		"--address="+address,
		"--account=reader",
		"--password-stdin",
		"--output", archive,
		"--result=json",
	)
	if denied.ExitCode != 4 {
		t.Fatalf("denied backup exit = %d, want 4; result=%#v", denied.ExitCode, denied)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(denied.Stdout), &result); err != nil {
		t.Fatalf("decode denied result: %v", err)
	}
	if result["exit_class"] != "access" {
		t.Fatalf("denied result = %#v", result)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("denied backup left artifact: %v", err)
	}
}

func TestOnlineBackupCreateAcceptsPasswordStdinAddressResultJSON(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "stdin-backup-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	archive := filepath.Join(t.TempDir(), "stdin.tar")
	created := runner.RunWithStdin(context.Background(), password+"\n",
		"backup", "create",
		"--address="+address,
		"--account=admin",
		"--password-stdin",
		"--output", archive,
		"--result=json",
	)
	if created.ExitCode != 0 {
		t.Fatalf("password-stdin online backup: %#v", created)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(created.Stdout), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["exit_class"] != "success" || result["complete"] != true {
		t.Fatalf("result = %#v", result)
	}
}

func assertOnlineBackupProgress(t *testing.T, stderr string) {
	t.Helper()
	want := []string{"connecting", "capturing", "writing", "validating"}
	dec := json.NewDecoder(strings.NewReader(stderr))
	phases := make([]string, 0, len(want))
	for dec.More() {
		var record map[string]any
		if err := dec.Decode(&record); err != nil {
			t.Fatalf("decode progress: %v stderr=%q", err, stderr)
		}
		if record["schema"] != "database.operator.progress/v1" {
			continue
		}
		phase, _ := record["phase"].(string)
		phases = append(phases, phase)
	}
	if len(phases) != len(want) {
		t.Fatalf("progress phases = %#v, want %#v", phases, want)
	}
	for index, phase := range want {
		if phases[index] != phase {
			t.Fatalf("progress phases = %#v, want %#v", phases, want)
		}
	}
}
