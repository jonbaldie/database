package blackbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue497ServeProgressAndSuccessDetails(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("issue-497-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	initialized := runner.Run(context.Background(), "init",
		"--data-directory", directory,
		"--initial-account", "admin",
		"--initial-password-file", passwordFile,
		"--result=json",
		"--progress=json",
	)
	if initialized.ExitCode != 0 {
		t.Fatalf("initialize instance: %#v", initialized)
	}
	var initializedResult struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal([]byte(initialized.Stdout), &initializedResult); err != nil {
		t.Fatalf("decode init result %q: %v", initialized.Stdout, err)
	}
	instanceID, _ := initializedResult.Details["instance_id"].(string)
	if instanceID == "" {
		t.Fatalf("init result has no instance identity: %#v", initializedResult.Details)
	}

	diagnosticsAddress := freeAddress(t)
	serverContext, cancelServer := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelServer()
	process, err := runner.Start(serverContext,
		"serve",
		"--data-directory", directory,
		"--mysql-listen-address", freeAddress(t),
		"--diagnostics-listen-address", diagnosticsAddress,
		"--result=json",
		"--progress=json",
	)
	if err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = process.Stop()
			_ = process.Wait()
		}
	}()
	waitForIssue497Readiness(t, diagnosticsAddress)
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	result := process.Wait()
	waited = true
	if result.ExitCode != 0 {
		t.Fatalf("graceful shutdown: %#v", result)
	}

	var phases []string
	for _, line := range strings.Split(strings.TrimSpace(result.Stderr), "\n") {
		var record struct {
			Schema     string `json:"schema"`
			RecordType string `json:"record_type"`
			Phase      string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Errorf("decode progress line %q: %v", line, err)
			continue
		}
		if record.Schema != "database.operator.progress/v1" || record.RecordType != "progress" {
			t.Errorf("unexpected progress record: %s", line)
			continue
		}
		phases = append(phases, record.Phase)
	}
	wantPhases := []string{"starting", "ready", "stopping"}
	if !equalIssue497Strings(phases, wantPhases) {
		t.Errorf("serve progress phases = %v, want %v", phases, wantPhases)
	}

	var terminal map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &terminal); err != nil {
		t.Fatalf("decode terminal result %q: %v", result.Stdout, err)
	}
	details, ok := terminal["details"].(map[string]any)
	if !ok {
		t.Fatalf("serve result details = %#v", terminal["details"])
	}
	if terminal["schema"] != "database.operator.result/v1" || terminal["command"] != "serve" || terminal["exit_class"] != "success" {
		t.Errorf("serve terminal result = %#v", terminal)
	}
	for _, key := range []string{"instance_id", "ready_at", "stopping_at", "shutdown_reason"} {
		if value, ok := details[key].(string); !ok || value == "" {
			t.Errorf("serve success details omit %q: %#v", key, details)
		}
	}
	if details["instance_id"] != instanceID {
		t.Errorf("serve instance identity = %v, want init identity %s", details["instance_id"], instanceID)
	}
	if details["state"] != "stopped" {
		t.Errorf("serve final state = %v, want stopped", details["state"])
	}
	if details["shutdown_reason"] != "SIGTERM" {
		t.Errorf("serve shutdown reason = %v, want SIGTERM", details["shutdown_reason"])
	}
	readyAt, readyOK := parseIssue497Time(t, details["ready_at"])
	stoppingAt, stoppingOK := parseIssue497Time(t, details["stopping_at"])
	if readyOK && stoppingOK && stoppingAt.Before(readyAt) {
		t.Errorf("serve stopping time %s is before readiness time %s", stoppingAt, readyAt)
	}
}

func waitForIssue497Readiness(t *testing.T, diagnosticsAddress string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ready map[string]string
		status, err := blackbox.HTTPJSON(ctx, diagnosticsAddress, "/ready", &ready)
		if err == nil && status == 200 && ready["status"] == "ready" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("serve did not become ready: status=%d response=%v err=%v", status, ready, err)
		case <-ticker.C:
		}
	}
}

func parseIssue497Time(t *testing.T, value any) (time.Time, bool) {
	t.Helper()
	text, ok := value.(string)
	if !ok || text == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Errorf("invalid RFC 3339 time %q: %v", text, err)
		return time.Time{}, false
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		t.Errorf("time %q is not UTC", text)
		return time.Time{}, false
	}
	return parsed, true
}

func equalIssue497Strings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
