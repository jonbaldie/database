package blackbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestOnlineCommandDiagnosticsDoNotRevealPasswordValidity(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	adminPassword := "issue-445-admin-secret"
	observerPassword := "issue-445-observer-secret"
	wrongPassword := "issue-445-wrong-secret"
	initializeServer(t, runner, directory, adminPassword)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", adminPassword)
	defer admin.close()
	mustQuery(t, admin, "CREATE USER 'observer' IDENTIFIED BY '"+observerPassword+"'")
	mustQuery(t, admin, "GRANT OPERATIONAL_OBSERVATION ON *.* TO 'observer'")

	backupOutput := filepath.Join(t.TempDir(), "denied.tar")
	commands := []struct {
		name string
		run  func(string) blackbox.Result
	}{
		{
			name: "backup create",
			run: func(password string) blackbox.Result {
				return runner.RunWithStdin(context.Background(), password+"\n",
					"backup", "create",
					"--address="+address,
					"--account=observer",
					"--password-stdin",
					"--output", backupOutput,
					"--result=json",
				)
			},
		},
		{
			name: "shutdown",
			run: func(password string) blackbox.Result {
				return runner.RunWithStdin(context.Background(), password+"\n",
					"shutdown",
					"--yes",
					"--address="+address,
					"--account=observer",
					"--password-stdin",
					"--result=json",
				)
			},
		},
	}

	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			validSummary := onlineAccessDiagnosticSummary(t, command.run(observerPassword))
			wrongSummary := onlineAccessDiagnosticSummary(t, command.run(wrongPassword))
			if validSummary != wrongSummary {
				t.Errorf("valid-password summary %q differs from wrong-password summary %q", validSummary, wrongSummary)
			}
		})
	}

	if result := admin.query("SELECT 1"); result.err != "" {
		t.Fatalf("server did not remain available after denied online commands: %v", result.err)
	}
	if _, err := os.Stat(backupOutput); !os.IsNotExist(err) {
		t.Fatalf("denied backup left artifact: %v", err)
	}
}

func onlineAccessDiagnosticSummary(t *testing.T, result blackbox.Result) string {
	t.Helper()
	if result.ExitCode != 4 {
		t.Fatalf("online command exit = %d, want 4; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	var envelope struct {
		ExitClass   string `json:"exit_class"`
		Diagnostics []struct {
			Summary string `json:"summary"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &envelope); err != nil {
		t.Fatalf("decode online command result: %v; stdout=%q", err, result.Stdout)
	}
	if envelope.ExitClass != "access" {
		t.Fatalf("online command exit class = %q, want access", envelope.ExitClass)
	}
	if len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Summary == "" {
		t.Fatalf("online command diagnostics = %#v, want one non-empty summary", envelope.Diagnostics)
	}
	return envelope.Diagnostics[0].Summary
}
