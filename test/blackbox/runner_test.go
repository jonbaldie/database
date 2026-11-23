package blackbox

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessSnapshotObservesStderrWhileWritesContinue(t *testing.T) {
	process := &Process{}
	writer := stderrWriter{output: &process.output}
	done := make(chan [2]string)
	go func() {
		_, _ = writer.Write([]byte("first\n"))
		_, _ = process.Snapshot()
		_, _ = writer.Write([]byte("second\n"))
		process.output.appendStdout("result")
		stdout, stderr := process.Snapshot()
		done <- [2]string{stdout, stderr}
	}()
	select {
	case got := <-done:
		if got[0] != "result\n" || got[1] != "first\nsecond\n" {
			t.Fatalf("snapshot = stdout %q stderr %q", got[0], got[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot and stderr writes did not release the output lock")
	}
}

// fakeServe writes an executable that prints script output for any command.
// It lets the instance tests reach each ready-event outcome without a server.
func fakeServe(t *testing.T, script string) Runner {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "database")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return Runner{Executable: executable}
}

const fakeReady = `{"schema":"database.lifecycle/v1","state":"ready","mysql_address":"127.0.0.1:4000","diagnostics_address":"127.0.0.1:4001"}`

func TestStartInstanceSkipsRecoveryAndStopsGracefully(t *testing.T) {
	runner := fakeServe(t, `trap 'exit 0' TERM
echo '{"state":"recovering"}'
echo '`+fakeReady+`'
while :; do sleep 0.05; done`)
	instance, err := StartInstance(context.Background(), runner, InstanceOptions{DataDirectory: "/unused", Diagnostics: true})
	if err != nil {
		t.Fatal(err)
	}
	if instance.MySQLAddress != "127.0.0.1:4000" || instance.DiagnosticsAddress != "127.0.0.1:4001" || instance.Ready["state"] != "ready" {
		t.Fatalf("instance = %#v", instance)
	}
	if result := instance.Stop(); result.ExitCode != 0 || result.Err != nil || !strings.Contains(result.Stdout, `"state":"ready"`) {
		t.Fatalf("stop = %#v", result)
	}
}

func TestStartInstanceRejectsAnyStateBeforeReady(t *testing.T) {
	runner := fakeServe(t, `echo '{"state":"failed"}'; echo 'serve failed' >&2; sleep 1`)
	_, err := StartInstance(context.Background(), runner, InstanceOptions{DataDirectory: "/unused"})
	if err == nil || !strings.Contains(err.Error(), "unexpected event before ready") || !strings.Contains(err.Error(), "serve failed") {
		t.Fatalf("error = %v, want the unexpected state and the process output", err)
	}
}

func TestStartInstanceReportsServeThatExitsOrStallsBeforeReady(t *testing.T) {
	if _, err := StartInstance(context.Background(), fakeServe(t, `exit 1`), InstanceOptions{DataDirectory: "/unused"}); !errors.Is(err, io.EOF) {
		t.Fatalf("exit before ready: error = %v, want EOF", err)
	}
	if _, err := StartInstance(context.Background(), fakeServe(t, `sleep 5`), InstanceOptions{DataDirectory: "/unused", ReadyTimeout: 50 * time.Millisecond}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stall before ready: error = %v, want the ready timeout", err)
	}
}

func TestStartInstanceRequiresTheBoundAddresses(t *testing.T) {
	mysqlOnly := `echo '{"state":"ready","mysql_address":"127.0.0.1:4000"}'; sleep 5`
	if instance, err := StartInstance(context.Background(), fakeServe(t, mysqlOnly), InstanceOptions{DataDirectory: "/unused"}); err != nil {
		t.Fatalf("MySQL only: error = %v", err)
	} else {
		_ = instance.Process.Crash()
		_ = instance.Process.Wait()
	}
	if _, err := StartInstance(context.Background(), fakeServe(t, mysqlOnly), InstanceOptions{DataDirectory: "/unused", Diagnostics: true}); err == nil || !strings.Contains(err.Error(), "bound listener addresses") {
		t.Fatalf("missing diagnostics: error = %v", err)
	}
	noMySQL := `echo '{"state":"ready","diagnostics_address":"127.0.0.1:4001"}'; sleep 5`
	if _, err := StartInstance(context.Background(), fakeServe(t, noMySQL), InstanceOptions{DataDirectory: "/unused", Diagnostics: true}); err == nil || !strings.Contains(err.Error(), "bound listener addresses") {
		t.Fatalf("missing MySQL: error = %v", err)
	}
}

func TestStartInstanceAndInitializeReportCommandFailures(t *testing.T) {
	missing := Runner{Executable: filepath.Join(t.TempDir(), "missing")}
	if _, err := StartInstance(context.Background(), missing, InstanceOptions{DataDirectory: "/unused"}); err == nil {
		t.Fatal("StartInstance with a missing executable succeeded")
	}
	err := Initialize(context.Background(), fakeServe(t, `echo 'init refused'; exit 3`), "/unused", "secret")
	if err == nil || !strings.Contains(err.Error(), "exit 3") || !strings.Contains(err.Error(), "init refused") {
		t.Fatalf("Initialize error = %v, want the exit code and output", err)
	}
	if err := Initialize(context.Background(), fakeServe(t, `exit 0`), "/unused", "secret"); err != nil {
		t.Fatalf("Initialize success: %v", err)
	}
}
