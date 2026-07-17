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
	DataDirectory      string
	MySQLAddress       string
	TLSCertFile        string
	TLSKeyFile         string
	DiagnosticsAddress string
	StateFile          string
	Format             string
	Timeouts
	ResourceLimits
	ConnectionLimits
	// MySQLEnabled distinguishes the compatibility diagnostics-only invocation
	// (serve with no instance and no explicit MySQL address) from the normal
	// configured server path.
	MySQLEnabled bool
}

type Timeouts struct {
	StatementTimeoutMilliseconds         int64
	LockWaitTimeoutMilliseconds          int64
	IdleInTransactionTimeoutMilliseconds int64
	IdleSessionTimeoutMilliseconds       int64
}

type ResourceLimits struct {
	ExecutionMemoryLimitBytes    int64
	AggregateMemoryLimitBytes    int64
	TemporaryStorageLimitBytes   int64
	AggregateTemporaryLimitBytes int64
}

type ConnectionLimits struct {
	MaxConnections       int
	MaxAllowedPacket     int64
	MaxPreparedStmtCount int
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
	server, err := newServer(opts, emit)
	if err != nil {
		return err
	}
	return server.serve(ctx)
}

type server struct {
	options Options
	emit    func(Event)
	health  *health
}

func newServer(opts Options, emit func(Event)) (*server, error) {
	if err := validateOptions(&opts); err != nil {
		return nil, err
	}
	return &server{options: opts, emit: emit, health: newHealth()}, nil
}

func validateOptions(opts *Options) error {
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
	return nil
}

func (s *server) serve(ctx context.Context) error {
	state, err := claimState(s.options.StateFile, s.options.DataDirectory)
	if err != nil {
		return err
	}
	cleanStop := false
	defer func() {
		state.finish(cleanStop)
	}()
	runtime, err := startRuntime(s.options, s.health)
	if err != nil {
		return err
	}
	s.reportReady(state.recovered, runtime.diagnosticsAddress)
	s.awaitStop(ctx)
	if err := runtime.closeGracefully(); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	cleanStop = true
	s.emit(Event{Schema: "database.lifecycle/v1", State: "stopped"})
	return nil
}

func (s *server) reportReady(recovered bool, diagnosticsAddress string) {
	s.health.set("ready")
	event := Event{Schema: "database.lifecycle/v1", State: "ready", Recovered: recovered}
	event.DiagnosticsAddress = diagnosticsAddress
	s.emit(event)
}

func (s *server) awaitStop(ctx context.Context) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-ctx.Done():
	case <-signals:
	}
	s.health.set("stopping")
	s.emit(Event{Schema: "database.lifecycle/v1", State: "stopping"})
}

type runtime struct {
	diagnostics        diagnosticsServer
	diagnosticsAddress string
	mysql              *mysql.Server
}

func startRuntime(opts Options, health *health) (runtime, error) {
	metadata, store, err := openServerData(opts.DataDirectory)
	if err != nil {
		return runtime{}, err
	}
	diagnostics, diagnosticsAddress, err := startDiagnostics(opts.DiagnosticsAddress, health)
	if err != nil {
		return runtime{}, err
	}
	mysqlServer, err := startMySQL(opts, metadata, store)
	if err != nil {
		_ = diagnostics.close()
		return runtime{}, err
	}
	return runtime{diagnostics: diagnostics, diagnosticsAddress: diagnosticsAddress, mysql: mysqlServer}, nil
}

func openServerData(directory string) (instance.Metadata, *catalog.Store, error) {
	if directory == "" {
		return instance.Metadata{}, nil, nil
	}
	metadata, err := instance.Load(directory)
	if err != nil {
		return instance.Metadata{}, nil, fmt.Errorf("load instance metadata: %w", err)
	}
	store, err := catalog.Open(directory)
	if err != nil {
		return instance.Metadata{}, nil, fmt.Errorf("open catalog: %w", err)
	}
	return metadata, store, nil
}

type diagnosticsServer struct {
	server *http.Server
}

func startDiagnostics(address string, health *health) (diagnosticsServer, string, error) {
	if address == "" {
		return diagnosticsServer{}, "", nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return diagnosticsServer{}, "", fmt.Errorf("listen for diagnostics: %w", err)
	}
	server := &http.Server{Handler: diagnosticsHandler(health)}
	go func() { _ = server.Serve(listener) }()
	return diagnosticsServer{server: server}, listener.Addr().String(), nil
}

func (d diagnosticsServer) close() error {
	if d.server == nil {
		return nil
	}
	return d.server.Close()
}

func startMySQL(opts Options, metadata instance.Metadata, store *catalog.Store) (*mysql.Server, error) {
	if opts.MySQLAddress == "" || !opts.MySQLEnabled {
		return nil, nil
	}
	config := mysql.Config{Catalog: store, Username: metadata.AdminAccount, PasswordHash: metadata.PasswordHash, TLSCertFile: opts.TLSCertFile, TLSKeyFile: opts.TLSKeyFile, MaxPreparedStmtCount: opts.MaxPreparedStmtCount, MaxAllowedPacket: opts.MaxAllowedPacket}
	server, err := mysql.NewWithConfig(opts.MySQLAddress, config)
	if err != nil {
		return nil, fmt.Errorf("listen for mysql: %w", err)
	}
	go server.Serve()
	return server, nil
}

func (r runtime) closeGracefully() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := closeMySQL(r.mysql)
	if r.diagnostics.server == nil {
		return err
	}
	if shutdownErr := r.diagnostics.server.Shutdown(shutdownCtx); shutdownErr != nil && err == nil {
		return shutdownErr
	}
	return err
}

func closeMySQL(server *mysql.Server) error {
	if server == nil {
		return nil
	}
	return server.CloseGracefully()
}

type claimedState struct {
	recovered bool
	path      string
	release   func() error
}

func claimState(path, dataDirectory string) (claimedState, error) {
	state, err := claimDataDirectory(dataDirectory)
	if err != nil {
		return claimedState{}, err
	}
	recovered, err := markStateRunning(path)
	if err != nil {
		state.finish(false)
		return claimedState{}, err
	}
	state.path = path
	state.recovered = recovered
	return state, nil
}

func claimDataDirectory(directory string) (claimedState, error) {
	if directory == "" {
		return claimedState{release: func() error { return nil }}, nil
	}
	file, err := os.OpenFile(filepath.Join(directory, ".running.lock"), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return claimedState{}, fmt.Errorf("claim data directory: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return claimedState{}, errors.New("data directory is already in use")
	}
	return claimedState{release: releaseFileLock(file)}, nil
}

func releaseFileLock(file *os.File) func() error {
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}
}

func markStateRunning(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create state directory: %w", err)
	}
	recovered, err := stateWasRunning(path)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte("running\n"), 0o644); err != nil {
		return false, fmt.Errorf("write state file: %w", err)
	}
	return recovered, nil
}

func stateWasRunning(path string) (bool, error) {
	contents, err := os.ReadFile(path)
	if err == nil {
		return string(contents) == "running\n", nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("read state file: %w", err)
}

func (s claimedState) finish(clean bool) {
	_ = s.release()
	if clean {
		_ = releaseState(s.path)
	}
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
