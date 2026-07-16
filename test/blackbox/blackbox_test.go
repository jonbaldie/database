package blackbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
