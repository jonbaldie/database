package blackbox_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestIssue545OperatorResultServeWarnsOnStandardErrorWhenReady(t *testing.T) {
	for _, progress := range []string{"json", "none"} {
		t.Run("progress="+progress, func(t *testing.T) {
			runner := blackbox.Runner{Executable: executable}
			directory := initializedInstance(t, runner)
			mysqlAddress := nonLoopbackAddress(t)
			diagnosticsAddress := freeAddress(t)
			process, err := runner.Start(context.Background(), "serve",
				"--data-directory="+directory,
				"--mysql-listen-address="+mysqlAddress,
				"--diagnostics-listen-address="+diagnosticsAddress,
				"--result=json", "--progress="+progress,
			)
			if err != nil {
				t.Fatal(err)
			}
			awaitReadyProbe(t, process, diagnosticsAddress)
			record, stdout := awaitReadyLifecycleRecord(t, process)
			if stdout != "" {
				t.Fatalf("stdout before stop = %q, want empty", stdout)
			}
			assertUnsafeListenerWarning(t, record["warnings"], mysqlAddress)

			stopped := stopServe(t, process)
			if strings.Count(stopped.Stdout, "\n") != 1 {
				t.Fatalf("stdout after stop = %q, want one result", stopped.Stdout)
			}
			var result struct {
				Schema  string         `json:"schema"`
				Details map[string]any `json:"details"`
			}
			if err := json.Unmarshal([]byte(stopped.Stdout), &result); err != nil || result.Schema != "database.operator.result/v1" {
				t.Fatalf("serve result = %#v (%v) stdout=%q", result, err, stopped.Stdout)
			}
			assertUnsafeListenerWarning(t, result.Details["warnings"], mysqlAddress)
			if progress == "json" {
				if phases := serveProgressPhases(t, stopped.Stderr); strings.Join(phases, ",") != "starting,ready,stopping" {
					t.Fatalf("serve progress phases = %v, want [starting ready stopping]", phases)
				}
			}
		})
	}
}

func TestIssue545OperatorResultServeOnLoopbackHasNoUnsafeListenerWarning(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	diagnosticsAddress := freeAddress(t)
	process, err := runner.Start(context.Background(), "serve",
		"--data-directory="+directory,
		"--mysql-listen-address="+freeAddress(t),
		"--diagnostics-listen-address="+diagnosticsAddress,
		"--result=json", "--progress=none",
	)
	if err != nil {
		t.Fatal(err)
	}
	awaitReadyProbe(t, process, diagnosticsAddress)
	record, _ := awaitReadyLifecycleRecord(t, process)
	if _, found := record["warnings"]; found {
		t.Fatalf("loopback ready record = %#v, want no warnings", record)
	}
	stopped := stopServe(t, process)
	if strings.Contains(stopped.Stdout+stopped.Stderr, "UNSAFE_NON_TLS_LISTENER") {
		t.Fatalf("loopback serve warned: stdout=%q stderr=%q", stopped.Stdout, stopped.Stderr)
	}
}

func TestIssue545LegacyServeOutputStillWarnsBeforeStop(t *testing.T) {
	for _, format := range []string{"json", "human"} {
		t.Run("format="+format, func(t *testing.T) {
			runner := blackbox.Runner{Executable: executable}
			directory := initializedInstance(t, runner)
			mysqlAddress := nonLoopbackAddress(t)
			diagnosticsAddress := freeAddress(t)
			args := []string{"serve",
				"--data-directory=" + directory,
				"--mysql-listen-address=" + mysqlAddress,
				"--diagnostics-listen-address=" + diagnosticsAddress,
			}
			if format == "json" {
				args = append(args, "--format=json")
			}
			process, err := runner.Start(context.Background(), args...)
			if err != nil {
				t.Fatal(err)
			}
			awaitReadyProbe(t, process, diagnosticsAddress)
			stdout := awaitStdoutContaining(t, process, "ready")
			if format == "human" {
				warning := strings.Index(stdout, "database: WARNING [UNSAFE_NON_TLS_LISTENER]")
				if warning < 0 || warning > strings.Index(stdout, "database: ready") {
					t.Fatalf("human stdout before stop = %q, want the warning before the ready line", stdout)
				}
			} else {
				assertUnsafeListenerWarning(t, readyLifecycleRecord(t, stdout)["warnings"], mysqlAddress)
			}
			stopServe(t, process)
		})
	}
}

func nonLoopbackAddress(t *testing.T) string {
	t.Helper()
	_, port, err := net.SplitHostPort(freeAddress(t))
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("0.0.0.0", port)
}

func awaitReadyLifecycleRecord(t *testing.T, process *blackbox.Process) (map[string]any, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stdout, stderr := process.Snapshot()
		if strings.Contains(stderr, "database.lifecycle/v1") {
			return readyLifecycleRecord(t, stderr), stdout
		}
		if time.Now().After(deadline) {
			_ = process.Crash()
			t.Fatalf("no ready lifecycle record on stderr before stop: stdout=%q stderr=%q", stdout, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func awaitStdoutContaining(t *testing.T, process *blackbox.Process, text string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stdout, stderr := process.Snapshot()
		if strings.Contains(stdout, text) {
			return stdout
		}
		if time.Now().After(deadline) {
			_ = process.Crash()
			t.Fatalf("stdout before stop has no %q: stdout=%q stderr=%q", text, stdout, stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readyLifecycleRecord(t *testing.T, stream string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(stream))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode stream: %v stream=%q", err, stream)
		}
		if record["schema"] == "database.lifecycle/v1" && record["state"] == "ready" {
			return record
		}
	}
	t.Fatalf("no ready database.lifecycle/v1 record in %q", stream)
	return nil
}

func assertUnsafeListenerWarning(t *testing.T, value any, address string) {
	t.Helper()
	warnings, _ := value.([]any)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one UNSAFE_NON_TLS_LISTENER warning", value)
	}
	warning, _ := warnings[0].(map[string]any)
	context, _ := warning["context"].(map[string]any)
	if warning["code"] != "UNSAFE_NON_TLS_LISTENER" || warning["severity"] != "warning" ||
		context["address"] != address || context["tls"] != "disabled" {
		t.Fatalf("warning = %#v, want UNSAFE_NON_TLS_LISTENER for %s with tls=disabled", warning, address)
	}
}

func stopServe(t *testing.T, process *blackbox.Process) blackbox.Result {
	t.Helper()
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped := process.Wait()
	if stopped.ExitCode != 0 {
		t.Fatalf("serve after SIGTERM: %#v", stopped)
	}
	return stopped
}
