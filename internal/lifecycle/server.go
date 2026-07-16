// Package lifecycle provides the minimal process and diagnostics seam used by
// the executable delivery spine. Database features attach to this seam later.
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// Options controls the process-level server seam. An empty DiagnosticsAddress
// disables the diagnostics listener, which keeps the default command useful
// for smoke tests and local process supervision.
type Options struct {
	DiagnosticsAddress string
	StateFile          string
	Format             string
}

// Event is emitted once the process has reached a lifecycle state. It is
// intentionally small: durable database and query state is owned by later
// feature seams.
type Event struct {
	Schema             string `json:"schema"`
	State              string `json:"state"`
	DiagnosticsAddress string `json:"diagnostics_address,omitempty"`
	Recovered          bool   `json:"recovered,omitempty"`
}

// Serve runs until it receives SIGINT or SIGTERM. It returns only after the
// diagnostics listener is closed and the state marker records a clean stop.
func Serve(ctx context.Context, opts Options, emit func(Event)) error {
	if opts.Format == "" {
		opts.Format = "human"
	}
	if opts.Format != "human" && opts.Format != "json" {
		return fmt.Errorf("unsupported format %q", opts.Format)
	}

	recovered, err := claimState(opts.StateFile)
	if err != nil {
		return err
	}
	cleanStop := false
	defer func() {
		if cleanStop {
			_ = releaseState(opts.StateFile)
		}
	}()

	var listener net.Listener
	var httpServer *http.Server
	if opts.DiagnosticsAddress != "" {
		listener, err = net.Listen("tcp", opts.DiagnosticsAddress)
		if err != nil {
			return fmt.Errorf("listen for diagnostics: %w", err)
		}
		httpServer = &http.Server{Handler: diagnosticsHandler()}
		go func() { _ = httpServer.Serve(listener) }()
	}

	ready := Event{Schema: "database.lifecycle/v1", State: "ready", Recovered: recovered}
	if listener != nil {
		ready.DiagnosticsAddress = listener.Addr().String()
	}
	emit(ready)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-ctx.Done():
	case <-signals:
	}
	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}
	cleanStop = true
	emit(Event{Schema: "database.lifecycle/v1", State: "stopped"})
	return nil
}

func claimState(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create state directory: %w", err)
	}
	recovered := false
	if contents, err := os.ReadFile(path); err == nil {
		recovered = string(contents) == "running\n"
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read state file: %w", err)
	}
	if err := os.WriteFile(path, []byte("running\n"), 0o644); err != nil {
		return false, fmt.Errorf("write state file: %w", err)
	}
	return recovered, nil
}

func releaseState(path string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte("stopped\n"), 0o644)
}

func diagnosticsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE database_process_ready gauge\ndatabase_process_ready 1\n"))
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
