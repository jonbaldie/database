package blackbox_test

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestDiagnosticsHTTPContractIsObservableEndToEnd(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	initializeServer(t, runner, directory, "diagnostics-secret")
	diagnostics := freeAddress(t)
	process, err := runner.Start(context.Background(), "serve", "--data-directory", directory, "--mysql-listen-address", freeAddress(t), "--diagnostics-listen-address", diagnostics, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Stop()
		_ = process.Wait()
	}()
	nextReadyEvent(t, process)

	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range []string{"/live", "/ready", "/metrics"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request, err := http.NewRequest(method, "http://"+diagnostics+path, nil)
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
		request, err := http.NewRequest(http.MethodPost, "http://"+diagnostics+path, nil)
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

	response, err := client.Get("http://" + diagnostics + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(metrics)
	if !strings.Contains(text, "database_server_ready 1") || strings.Contains(text, "operation_id") || strings.Contains(text, "session_id") || strings.Contains(text, "query=") {
		t.Fatalf("unexpected metrics exposition: %q", text)
	}
}
