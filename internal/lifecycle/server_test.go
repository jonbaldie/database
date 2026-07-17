package lifecycle

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/instance"
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
	status, response = healthResponse(t, listener.Addr().String(), "/live")
	if status != http.StatusOK || response["status"] != "live" {
		t.Fatalf("shutdown liveness = status %d body %#v", status, response)
	}
	_ = server.Shutdown(context.Background())
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
