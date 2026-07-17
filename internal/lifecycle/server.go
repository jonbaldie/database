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
	"sync"
	"syscall"
	"time"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/instance"
	"github.com/jonbaldie/database/internal/mysql"
)

// Options controls the process-level server seam. An empty DiagnosticsAddress
// disables the diagnostics listener, which keeps the default command useful
// for smoke tests and local process supervision.
type Options struct {
	DataDirectory                        string
	MySQLAddress                         string
	TLSCertFile                          string
	TLSKeyFile                           string
	DiagnosticsAddress                   string
	StateFile                            string
	Format                               string
	StatementTimeoutMilliseconds         int64
	LockWaitTimeoutMilliseconds          int64
	IdleInTransactionTimeoutMilliseconds int64
	IdleSessionTimeoutMilliseconds       int64
	ExecutionMemoryLimitBytes            int64
	AggregateMemoryLimitBytes            int64
	TemporaryStorageLimitBytes           int64
	AggregateTemporaryLimitBytes         int64
	MaxConnections                       int
	MaxAllowedPacket                     int64
	MaxPreparedStmtCount                 int
	// MySQLEnabled distinguishes the compatibility diagnostics-only invocation
	// (serve with no instance and no explicit MySQL address) from the normal
	// configured server path.
	MySQLEnabled bool
}

// Event is emitted once the process has reached a lifecycle state. It is
// intentionally small: durable database and query state is owned by later
// feature seams.
type Event struct {
	Schema             string `json:"schema"`
	State              string `json:"state"`
	OperationID        string `json:"operation_id,omitempty"`
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
	if (opts.TLSCertFile == "") != (opts.TLSKeyFile == "") {
		return errors.New("TLS certificate and private key must be provided together")
	}
	if opts.DiagnosticsAddress != "" && opts.DiagnosticsAddress == opts.MySQLAddress {
		return errors.New("MySQL and diagnostics listeners must use different addresses")
	}

	if opts.DataDirectory != "" {
		if err := validateInstance(opts.DataDirectory); err != nil {
			return err
		}
	}
	recovered, release, err := claimState(opts.StateFile, opts.DataDirectory)
	if err != nil {
		return err
	}
	cleanStop := false
	defer func() {
		_ = release()
		if cleanStop {
			_ = releaseState(opts.StateFile)
		}
	}()

	var listener net.Listener
	var mysqlServer *mysql.Server
	var httpServer *http.Server
	health := newHealth()
	var metadata instance.Metadata
	var store *catalog.Store
	if opts.DataDirectory != "" {
		metadata, err = instance.Load(opts.DataDirectory)
		if err != nil {
			return fmt.Errorf("load instance metadata: %w", err)
		}
		store, err = catalog.Open(opts.DataDirectory)
		if err != nil {
			return fmt.Errorf("open catalog: %w", err)
		}
	}
	if opts.DiagnosticsAddress != "" {
		listener, err = net.Listen("tcp", opts.DiagnosticsAddress)
		if err != nil {
			return fmt.Errorf("listen for diagnostics: %w", err)
		}
		httpServer = &http.Server{Handler: diagnosticsHandler(health)}
		go func() { _ = httpServer.Serve(listener) }()
	}
	if opts.MySQLAddress != "" && opts.MySQLEnabled {
		mysqlServer, err = mysql.NewWithConfig(opts.MySQLAddress, mysql.Config{Catalog: store, Username: metadata.AdminAccount, PasswordHash: metadata.PasswordHash, TLSCertFile: opts.TLSCertFile, TLSKeyFile: opts.TLSKeyFile})
		if err != nil {
			return fmt.Errorf("listen for mysql: %w", err)
		}
		go mysqlServer.Serve()
	}

	health.set("ready")
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
	health.set("stopping")
	emit(Event{Schema: "database.lifecycle/v1", State: "stopping"})
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var shutdownErr error
	if mysqlServer != nil {
		shutdownErr = mysqlServer.CloseGracefully(shutdownCtx)
	}
	if httpServer != nil {
		if err := httpServer.Shutdown(shutdownCtx); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
	}
	if shutdownErr != nil {
		return fmt.Errorf("graceful shutdown: %w", shutdownErr)
	}
	cleanStop = true
	emit(Event{Schema: "database.lifecycle/v1", State: "stopped"})
	return nil
}

func claimState(path, dataDirectory string) (bool, func() error, error) {
	release := func() error { return nil }
	lockPath := ""
	if dataDirectory != "" {
		lockPath = filepath.Join(dataDirectory, ".running.lock")
	} else if path == "" {
		return false, release, nil
	}
	if lockPath != "" {
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			return false, release, fmt.Errorf("claim data directory: %w", err)
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = file.Close()
			return false, release, errors.New("data directory is already in use")
		}
		release = func() error {
			unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			closeErr := file.Close()
			if unlockErr != nil {
				return unlockErr
			}
			return closeErr
		}
	}
	if path == "" {
		return false, release, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		_ = release()
		return false, func() error { return nil }, fmt.Errorf("create state directory: %w", err)
	}
	recovered := false
	if contents, err := os.ReadFile(path); err == nil {
		recovered = string(contents) == "running\n"
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = release()
		return false, func() error { return nil }, fmt.Errorf("read state file: %w", err)
	}
	if err := os.WriteFile(path, []byte("running\n"), 0o644); err != nil {
		_ = release()
		return false, func() error { return nil }, fmt.Errorf("write state file: %w", err)
	}
	return recovered, release, nil
}

func validateInstance(directory string) error {
	info, err := os.Stat(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("data directory does not exist")
		}
		return fmt.Errorf("inspect data directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("data directory is not a directory")
	}
	instanceMetadata, err := instance.Load(directory)
	if err != nil {
		return errors.New("data directory is not initialized")
	}
	if instanceMetadata.State != "stopped" {
		return errors.New("data directory has invalid instance metadata")
	}
	catalogPath := filepath.Join(directory, "catalog.json")
	info, err = os.Stat(catalogPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("data directory has missing or invalid catalog")
	}
	if _, err := catalog.Open(directory); err != nil {
		return errors.New("data directory has damaged catalog")
	}
	return nil
}

func releaseState(path string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte("stopped\n"), 0o644)
}

type health struct {
	mu    sync.RWMutex
	state string
}

func newHealth() *health { return &health{state: "starting"} }

func (h *health) set(state string) {
	h.mu.Lock()
	h.state = state
	h.mu.Unlock()
}

func (h *health) current() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state
}

func diagnosticsHandler(health *health) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if health.current() == "ready" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": health.current()})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		ready := "0"
		if health.current() == "ready" {
			ready = "1"
		}
		_, _ = w.Write([]byte("# TYPE database_process_ready gauge\ndatabase_process_ready " + ready + "\n"))
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
