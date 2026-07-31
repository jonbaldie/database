package lifecycle

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/instance"
	"github.com/jonbaldie/database/internal/mysql"
)

func TestServeRejectsDamagedInitializedDirectory(t *testing.T) {
	directory := initializedDirectory(t)
	if err := os.Remove(filepath.Join(directory, "catalog.json")); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), Options{DataDirectory: directory}, func(Event) {}); err == nil {
		t.Fatal("Serve accepted an initialized directory whose catalog is missing")
	}
}

func TestServeRejectsStructurallyDamagedCatalog(t *testing.T) {
	directory := initializedDirectory(t)
	if err := os.WriteFile(filepath.Join(directory, "catalog.json"), []byte(`{"namespaces":{"bad":null}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), Options{DataDirectory: directory}, func(Event) {}); err == nil {
		t.Fatal("Serve accepted a catalog with an invalid namespace entry")
	}
}

func TestServeRejectsUpgradeIncompleteDirectory(t *testing.T) {
	directory := initializedDirectory(t)
	if err := os.WriteFile(filepath.Join(directory, instance.UpgradeIncompleteMarker), []byte(`{"schema":"database.upgrade/v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), Options{DataDirectory: directory}, func(Event) {}); err == nil {
		t.Fatal("Serve accepted a directory with an incomplete upgrade marker")
	}
}

func TestServeRejectsIncompleteDurableRecoveryArtifacts(t *testing.T) {
	directory := initializedDirectory(t)
	if err := os.WriteFile(filepath.Join(directory, ".catalog-crash.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), Options{DataDirectory: directory}, func(Event) {}); err == nil {
		t.Fatal("Serve accepted an incomplete catalog commit")
	}
}

func TestServeRejectsConcurrentOwnerAndAllowsRestart(t *testing.T) {
	directory := initializedDirectory(t)
	contextOne, stopOne := context.WithCancel(context.Background())
	ready := make(chan Event, 1)
	done := make(chan error, 1)
	go func() {
		done <- Serve(contextOne, Options{DataDirectory: directory}, func(event Event) {
			if event.State == "ready" {
				ready <- event
			}
		})
	}()
	select {
	case event := <-ready:
		if event.State != "ready" {
			t.Fatalf("state = %q, want ready", event.State)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first server did not become ready")
	}
	if err := Serve(context.Background(), Options{DataDirectory: directory}, func(Event) {}); err == nil {
		t.Fatal("Serve accepted a data directory already owned by another server")
	}
	stopOne()
	if err := <-done; err != nil {
		t.Fatalf("first server shutdown: %v", err)
	}

	contextTwo, stopTwo := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- Serve(contextTwo, Options{DataDirectory: directory}, func(Event) {}) }()
	time.Sleep(30 * time.Millisecond)
	stopTwo()
	if err := <-done; err != nil {
		t.Fatalf("restarted server: %v", err)
	}
}

func TestDiagnosticsReadinessTracksStartupAndShutdown(t *testing.T) {
	health := newHealth()
	handler := diagnosticsHandler(health)
	server := &http.Server{Handler: handler}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	status, response := healthResponse(t, listener.Addr().String(), "/ready")
	if status != http.StatusServiceUnavailable || response["reason"] != "starting" {
		t.Fatalf("startup readiness = status %d body %#v", status, response)
	}
	health.set("ready")
	status, response = healthResponse(t, listener.Addr().String(), "/ready")
	if status != http.StatusOK || response["status"] != "ready" {
		t.Fatalf("ready response = status %d body %#v", status, response)
	}
	health.set("stopping")
	status, response = healthResponse(t, listener.Addr().String(), "/ready")
	if status != http.StatusServiceUnavailable || response["reason"] != "stopping" {
		t.Fatalf("shutdown readiness = status %d body %#v", status, response)
	}
	for _, reason := range []string{"recovering", "shutting_down", "corruption"} {
		health.set(reason)
		status, response = healthResponse(t, listener.Addr().String(), "/ready")
		if status != http.StatusServiceUnavailable || response["reason"] != reason {
			t.Fatalf("%s readiness = status %d body %#v", reason, status, response)
		}
	}
	status, response = healthResponse(t, listener.Addr().String(), "/live")
	if status != http.StatusOK || response["status"] != "live" {
		t.Fatalf("shutdown liveness = status %d body %#v", status, response)
	}
	_ = server.Shutdown(context.Background())
}

func TestDiagnosticsMethodsAndMetricsAreBounded(t *testing.T) {
	health := newHealth()
	health.set("ready")
	server := httptest.NewServer(diagnosticsHandler(health))
	defer server.Close()
	client := server.Client()

	for _, path := range []string{"/live", "/ready", "/metrics"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request, err := http.NewRequest(method, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("%s %s status=%d, want 200", method, path, response.StatusCode)
			}
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status=%d, want 405", path, response.StatusCode)
		}
	}

	response, err := client.Get(server.URL + "/unknown")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path status=%d, want 404", response.StatusCode)
	}

	response, err = client.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(metrics)
	if response.Header.Get("Content-Type") != "text/plain; version=0.0.4" || !strings.Contains(text, "database_server_ready 1") {
		t.Fatalf("metrics headers/body = %q / %q", response.Header.Get("Content-Type"), text)
	}
	for _, prohibited := range []string{"operation_id", "session_id", "account=", "query=", "namespace="} {
		if strings.Contains(text, prohibited) {
			t.Fatalf("metrics contain prohibited identifier %q: %q", prohibited, text)
		}
	}
}

func TestLifecycleEventsHaveStableCodesAndOperationIdentity(t *testing.T) {
	directory := initializedDirectory(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	events := make(chan Event, 4)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{DataDirectory: directory, OperationID: "op-test"}, func(event Event) { events <- event })
	}()
	ready := receiveEvent(t, events, "ready")
	if ready.EventCode != "server.ready" || ready.Severity != "info" || ready.OperationID != "op-test" {
		stop()
		<-done
		t.Fatalf("ready event = %#v", ready)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stopping := receiveEvent(t, events, "stopping")
	stopped := receiveEvent(t, events, "stopped")
	if stopping.EventCode != "server.stopping" || stopped.EventCode != "server.stopped" || stopping.OperationID != "op-test" || stopped.OperationID != "op-test" {
		t.Fatalf("shutdown events = %#v / %#v", stopping, stopped)
	}
}

func TestDiagnosticResourceUsageHandlesDiagnosticsWithoutMySQL(t *testing.T) {
	usage := diagnosticResourceUsage([]*mysql.Server{nil})
	if usage != (mysql.ResourceUsage{}) {
		t.Fatalf("diagnostics-only resource usage = %#v", usage)
	}
}

func TestServeReportsReadinessAndWritesCleanStateOnShutdown(t *testing.T) {
	directory := initializedDirectory(t)
	stateFile := filepath.Join(t.TempDir(), "server.state")
	if err := os.WriteFile(stateFile, []byte("running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	events := make(chan Event, 3)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{
			DataDirectory:      directory,
			DiagnosticsAddress: "127.0.0.1:0",
			StateFile:          stateFile,
		}, func(event Event) { events <- event })
	}()
	recovering := receiveEvent(t, events, "recovering")
	if !recovering.Recovered {
		t.Fatal("recovery event did not report recovery")
	}
	ready := receiveEvent(t, events, "ready")
	if !ready.Recovered {
		t.Fatal("ready event did not report recovery from the running state")
	}
	status, response := healthResponse(t, ready.DiagnosticsAddress, "/ready")
	if status != http.StatusOK || response["status"] != "ready" {
		t.Fatalf("running readiness = status %d body %#v", status, response)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Serve shutdown: %v", err)
	}
	receiveEvent(t, events, "stopping")
	receiveEvent(t, events, "stopped")
	contents, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "stopped\n" {
		t.Fatalf("state after clean shutdown = %q, want stopped", contents)
	}
}

func TestServeReportsStructuredWarningForNonLoopbackMySQLWithoutTLS(t *testing.T) {
	directory := initializedDirectory(t)
	ctx, stop := context.WithCancel(context.Background())
	events := make(chan Event, 4)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{
			DataDirectory: directory,
			MySQLAddress:  "0.0.0.0:0",
			MySQLEnabled:  true,
		}, func(event Event) { events <- event })
	}()
	ready := receiveEvent(t, events, "ready")
	if len(ready.Warnings) != 1 {
		stop()
		<-done
		t.Fatalf("ready warnings = %#v, want one warning", ready.Warnings)
	}
	warning := ready.Warnings[0]
	if warning.Code != "UNSAFE_NON_TLS_LISTENER" || warning.Severity != "warning" ||
		warning.Context["address"] != "0.0.0.0:0" || warning.Context["tls"] != "disabled" {
		stop()
		<-done
		t.Fatalf("warning = %#v", warning)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Serve shutdown: %v", err)
	}
}

func TestServeDoesNotWarnForLoopbackMySQLWithoutTLS(t *testing.T) {
	directory := initializedDirectory(t)
	ctx, stop := context.WithCancel(context.Background())
	events := make(chan Event, 4)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{
			DataDirectory: directory,
			MySQLAddress:  "127.0.0.1:0",
			MySQLEnabled:  true,
		}, func(event Event) { events <- event })
	}()
	ready := receiveEvent(t, events, "ready")
	if len(ready.Warnings) != 0 {
		stop()
		<-done
		t.Fatalf("loopback ready warnings = %#v, want none", ready.Warnings)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Serve shutdown: %v", err)
	}
}

func TestServeReleasesDataDirectoryClaimWhenStateMarkerFails(t *testing.T) {
	directory := initializedDirectory(t)
	stateParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(stateParent, []byte("file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(stateParent, "server.state")
	if err := Serve(context.Background(), Options{DataDirectory: directory, StateFile: stateFile}, func(Event) {}); err == nil {
		t.Fatal("Serve accepted a state file whose parent is not a directory")
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if err := Serve(ctx, Options{DataDirectory: directory}, func(Event) {}); err != nil {
		t.Fatalf("Serve did not release the failed state claim: %v", err)
	}
}

func initializedDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "instance")
	if _, err := instance.Initialize(directory, "admin", "test-password"); err != nil {
		t.Fatal(err)
	}
	return directory
}

func healthResponse(t *testing.T, address, path string) (int, map[string]string) {
	t.Helper()
	response, err := http.Get("http://" + address + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body
}

func receiveEvent(t *testing.T, events <-chan Event, state string) Event {
	t.Helper()
	select {
	case event := <-events:
		if event.State != state {
			t.Fatalf("event state = %q, want %q", event.State, state)
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatalf("did not receive %q event", state)
		return Event{}
	}
}
