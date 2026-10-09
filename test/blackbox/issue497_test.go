package blackbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue497ServeProgressAndResultFollowTheOperatorContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	diagnosticsAddress := freeAddress(t)
	process, err := runner.Start(context.Background(),
		"serve",
		"--data-directory="+directory,
		"--mysql-listen-address="+freeAddress(t),
		"--diagnostics-listen-address="+diagnosticsAddress,
		"--result=json",
		"--progress=json",
	)
	if err != nil {
		t.Fatal(err)
	}
	awaitReadyProbe(t, process, diagnosticsAddress)
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped := process.Wait()
	if stopped.ExitCode != 0 {
		t.Fatalf("serve after SIGTERM: %#v", stopped)
	}

	if phases := serveProgressPhases(t, stopped.Stderr); strings.Join(phases, ",") != "starting,ready,stopping" {
		t.Fatalf("serve progress phases = %v, want [starting ready stopping]", phases)
	}
	var result struct {
		ExitClass string         `json:"exit_class"`
		Details   map[string]any `json:"details"`
	}
	if err := json.Unmarshal([]byte(stopped.Stdout), &result); err != nil {
		t.Fatalf("decode serve result: %v stdout=%q", err, stopped.Stdout)
	}
	if result.ExitClass != "success" {
		t.Fatalf("serve result = %#v", result)
	}
	instance := inspectInstanceID(t, runner, directory)
	details := result.Details
	if details["instance_id"] != instance || details["state"] != "stopped" || details["shutdown_reason"] != "signal" {
		t.Fatalf("serve details = %#v, want instance_id %q, state stopped, shutdown_reason signal", details, instance)
	}
	readyAt := detailTime(t, details, "ready_at")
	stoppingAt := detailTime(t, details, "stopping_at")
	if stoppingAt.Before(readyAt) {
		t.Fatalf("stopping_at %s is before ready_at %s", stoppingAt, readyAt)
	}
}

func awaitReadyProbe(t *testing.T, process *blackbox.Process, diagnosticsAddress string) {
	t.Helper()
	client := http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if response, err := client.Get("http://" + diagnosticsAddress + "/ready"); err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = process.Crash()
	t.Fatalf("serve never became ready on %s: %#v", diagnosticsAddress, process.Wait())
}

func serveProgressPhases(t *testing.T, stderr string) []string {
	t.Helper()
	var phases []string
	decoder := json.NewDecoder(strings.NewReader(stderr))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode progress: %v stderr=%q", err, stderr)
		}
		if record["schema"] == "database.operator.progress/v1" {
			phase, _ := record["phase"].(string)
			phases = append(phases, phase)
		}
	}
	return phases
}

func inspectInstanceID(t *testing.T, runner blackbox.Runner, directory string) string {
	t.Helper()
	inspected := runner.Run(context.Background(), "data", "inspect", "--data-directory="+directory, "--result=json")
	var result struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal([]byte(inspected.Stdout), &result); err != nil {
		t.Fatalf("decode data inspect: %v %#v", err, inspected)
	}
	instance, _ := result.Details["instance_id"].(string)
	if instance == "" {
		t.Fatalf("data inspect has no instance_id: %#v", result)
	}
	return instance
}

func detailTime(t *testing.T, details map[string]any, key string) time.Time {
	t.Helper()
	text, _ := details[key].(string)
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || value.Location() != time.UTC {
		t.Fatalf("details[%q] = %#v, want a UTC RFC 3339 time", key, details[key])
	}
	return value
}
