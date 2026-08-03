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

func TestOperatorShutdownStopsRunningServerWithResultAndProgress(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "operator-shutdown-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)

	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopped := runner.Run(context.Background(),
		"shutdown",
		"--yes",
		"--address="+address,
		"--account=admin",
		"--password-file", passwordFile,
		"--result=json",
		"--progress=json",
	)
	if stopped.ExitCode != 0 {
		t.Fatalf("operator shutdown: %#v", stopped)
	}
	assertShutdownProgress(t, stopped.Stderr)
	var result map[string]any
	if err := json.Unmarshal([]byte(stopped.Stdout), &result); err != nil {
		t.Fatalf("decode shutdown result: %v stdout=%q", err, stopped.Stdout)
	}
	if result["exit_class"] != "success" || result["command"] != "shutdown" {
		t.Fatalf("shutdown result = %#v", result)
	}
	if result["instance_id"] == "" || result["requested_at"] == "" || result["stopping_at"] == "" || result["state"] != "stopped" {
		t.Fatalf("shutdown details incomplete: %#v", result)
	}
	serveResult := process.Wait()
	if serveResult.ExitCode != 0 {
		t.Fatalf("serve after operator shutdown: %#v", serveResult)
	}

	restart, restartAddress := startMySQLServer(t, runner, directory)
	defer func() { _ = restart.Stop(); _ = restart.Wait() }()
	client := newWireClient(t, restartAddress, "admin", password)
	defer client.close()
	mustQuery(t, client, "SELECT 1")
}

func TestOperatorShutdownRequiresYes(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "shutdown-yes-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := runner.Run(context.Background(),
		"shutdown",
		"--address="+address,
		"--account=admin",
		"--password-file", passwordFile,
		"--result=json",
	)
	if denied.ExitCode != 2 {
		t.Fatalf("missing --yes exit = %d, want 2; %#v", denied.ExitCode, denied)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(denied.Stdout), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["exit_class"] != "invalid_input" {
		t.Fatalf("result = %#v", result)
	}
	diagnostic, _ := result["diagnostic"].(string)
	if !strings.Contains(diagnostic, "--yes") {
		t.Fatalf("diagnostic = %q", diagnostic)
	}
}

func TestOperatorShutdownRejectsMissingOperationalControl(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "shutdown-access-secret"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", password)
	defer admin.close()
	mustQuery(t, admin, "CREATE USER 'reader' IDENTIFIED BY 'reader-shutdown-secret'")
	mustQuery(t, admin, "GRANT OPERATIONAL_OBSERVATION ON *.* TO 'reader'")

	denied := runner.RunWithStdin(context.Background(), "reader-shutdown-secret\n",
		"shutdown",
		"--yes",
		"--address="+address,
		"--account=reader",
		"--password-stdin",
		"--result=json",
	)
	if denied.ExitCode != 4 {
		t.Fatalf("denied shutdown exit = %d, want 4; %#v", denied.ExitCode, denied)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(denied.Stdout), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["exit_class"] != "access" {
		t.Fatalf("denied result = %#v", result)
	}
}

func assertShutdownProgress(t *testing.T, stderr string) {
	t.Helper()
	want := []string{"connecting", "requesting", "draining", "stopped"}
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
