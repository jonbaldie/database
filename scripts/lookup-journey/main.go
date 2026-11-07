package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

const password = "performance-password"

type config struct {
	executable string
	rows       int
	payload    int
	sessions   int
	ops        int
	samples    int
	warmup     int
	label      string
}

type runningServer struct {
	command  *exec.Cmd
	address  string
	stderr   strings.Builder
	readDone sync.WaitGroup
	stopped  bool
	stopErr  error
}

func main() {
	log.SetFlags(0)
	cfg, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func parseConfig() (config, error) {
	root, err := os.Getwd()
	if err != nil {
		return config{}, err
	}
	cfg := config{}
	flags := flag.NewFlagSet("lookup-journey", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&cfg.executable, "executable", filepath.Join(root, "bin", "database"), "database executable")
	flags.IntVar(&cfg.rows, "rows", 40000, "narrow_records row count")
	flags.IntVar(&cfg.payload, "payload", 64, "payload byte count")
	flags.IntVar(&cfg.sessions, "sessions", 4, "concurrent prepared sessions")
	flags.IntVar(&cfg.ops, "ops", 100, "lookups per session in one sample")
	flags.IntVar(&cfg.samples, "samples", 7, "measured samples")
	flags.IntVar(&cfg.warmup, "warmup", 50, "untimed lookups per session")
	flags.StringVar(&cfg.label, "label", "target", "name printed with the metric")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if cfg.rows <= 0 || cfg.payload <= 0 || cfg.sessions <= 0 || cfg.ops <= 0 || cfg.samples <= 0 || cfg.warmup < 0 {
		return config{}, errors.New("rows, payload, sessions, ops, and samples must be positive")
	}
	if cfg.sessions > 100 {
		return config{}, errors.New("sessions cannot exceed the default server connection ceiling")
	}
	if cfg.payload > 16384 {
		return config{}, errors.New("payload cannot exceed the narrow_records column width")
	}
	return cfg, nil
}

func run(cfg config) (err error) {
	directory, err := os.MkdirTemp("", "lookup-journey-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	if err := initialize(cfg.executable, directory); err != nil {
		return err
	}
	server, err := startServer(cfg.executable, directory)
	if err != nil {
		return err
	}
	defer func() {
		if stopErr := server.stop(); err == nil {
			err = stopErr
		}
	}()
	fmt.Printf("server_pid=%d\n", server.command.Process.Pid)

	db, err := openDB(server.address, "")
	if err != nil {
		return err
	}
	if err := waitReady(db); err != nil {
		_ = db.Close()
		return err
	}
	if err := createSchema(db); err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	db, err = openDB(server.address, "lookup_journey")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.sessions)
	db.SetMaxIdleConns(cfg.sessions)
	if err := loadRows(db, cfg); err != nil {
		return err
	}

	sessions, err := prepareSessions(db, cfg.sessions)
	if err != nil {
		return err
	}
	defer closeSessions(sessions)
	if _, _, _, _, err := runLookups(sessions, cfg, cfg.warmup, false); err != nil {
		return err
	}

	medians := make([]time.Duration, 0, cfg.samples)
	errorsTotal := 0
	workTotal := 0
	for sample := 1; sample <= cfg.samples; sample++ {
		latencies, work, failures, first, err := runLookups(sessions, cfg, cfg.ops, true)
		if err != nil {
			return err
		}
		errorsTotal += failures
		workTotal += work
		median := medianDuration(latencies)
		medians = append(medians, median)
		fmt.Printf("sample=%d median_ms=%.3f work=%d errors=%d\n", sample, millis(median), work, failures)
		if first != "" {
			fmt.Printf("first_error=%s\n", first)
		}
	}
	summary := medianDuration(medians)
	low, high := medians[0], medians[0]
	for _, value := range medians {
		if value < low {
			low = value
		}
		if value > high {
			high = value
		}
	}
	fmt.Printf("lookup_journey label=%s median_ms=%.3f min_ms=%.3f max_ms=%.3f samples=%d errors=%d work=%d rows=%d payload=%d sessions=%d ops=%d\n",
		cfg.label, millis(summary), millis(low), millis(high), cfg.samples, errorsTotal, workTotal, cfg.rows, cfg.payload, cfg.sessions, cfg.ops)
	expected := cfg.sessions * cfg.ops * cfg.samples
	if errorsTotal != 0 || workTotal != expected {
		return fmt.Errorf("lookup journey failed: errors=%d work=%d expected_work=%d", errorsTotal, workTotal, expected)
	}
	return nil
}

func initialize(executable, directory string) error {
	command := exec.Command(executable, "init", directory, "--password-stdin", "--format=json")
	command.Stdin = strings.NewReader(password + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initialize instance: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func startServer(executable, directory string) (*runningServer, error) {
	address, err := freeAddress()
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, "serve", "--data-directory", directory, "--mysql-listen-address", address, "--format=json")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}
	server := &runningServer{command: command, address: address}
	server.readDone.Add(2)
	go func() {
		defer server.readDone.Done()
		_, _ = io.Copy(io.Discard, stdout)
	}()
	go func() {
		defer server.readDone.Done()
		_, _ = io.Copy(&server.stderr, stderr)
	}()
	return server, nil
}

func (server *runningServer) stop() error {
	if server == nil || server.command == nil || server.command.Process == nil || server.stopped {
		if server == nil {
			return nil
		}
		return server.stopErr
	}
	server.stopped = true
	if err := server.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		server.stopErr = fmt.Errorf("stop server: %w", err)
		return server.stopErr
	}
	err := server.command.Wait()
	server.readDone.Wait()
	if err != nil {
		server.stopErr = fmt.Errorf("wait for server: %w; stderr=%s", err, strings.TrimSpace(server.stderr.String()))
	}
	return server.stopErr
}

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate listener: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return address, nil
}

func openDB(address, database string) (*sql.DB, error) {
	configuration := mysql.Config{
		User: "admin", Passwd: password, Net: "tcp", Addr: address, DBName: database,
		ParseTime: true, MaxAllowedPacket: 64 << 20,
		Timeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second,
	}
	return sql.Open("mysql", configuration.FormatDSN())
}

func waitReady(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		if err := db.PingContext(ctx); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("server did not become ready: %w", ctx.Err())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func createSchema(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, "CREATE DATABASE lookup_journey"); err != nil {
		return fmt.Errorf("create lookup_journey database: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE lookup_journey.narrow_records (
		id BIGINT NOT NULL,
		unique_key VARCHAR(64) NOT NULL,
		secondary_key VARCHAR(64) NOT NULL,
		base_secondary_key VARCHAR(64) NOT NULL,
		numeric_value BIGINT NOT NULL,
		payload VARCHAR(16384) NOT NULL,
		PRIMARY KEY (id),
		UNIQUE KEY unique_narrow_key (unique_key),
		KEY secondary_narrow_key (secondary_key)
	)`); err != nil {
		return fmt.Errorf("create narrow_records: %w", err)
	}
	return nil
}

func loadRows(db *sql.DB, cfg config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	body := strings.Repeat("x", cfg.payload)
	const batchSize = 500
	for start := 1; start <= cfg.rows; start += batchSize {
		end := start + batchSize
		if end > cfg.rows+1 {
			end = cfg.rows + 1
		}
		var statement strings.Builder
		statement.WriteString("INSERT INTO narrow_records (id, unique_key, secondary_key, base_secondary_key, numeric_value, payload) VALUES ")
		for id := start; id < end; id++ {
			if id != start {
				statement.WriteByte(',')
			}
			secondary := fmt.Sprintf("secondary-%d", id%10000)
			fmt.Fprintf(&statement, "(%d,'unique-%d','%s','%s',0,'%s')", id, id, secondary, secondary, body)
		}
		if _, err := db.ExecContext(ctx, statement.String()); err != nil {
			return fmt.Errorf("load rows starting at %d: %w", start, err)
		}
	}
	return nil
}

type workerSession struct {
	conn  *sql.Conn
	query *sql.Stmt
}

func prepareSessions(db *sql.DB, count int) ([]workerSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sessions := make([]workerSession, 0, count)
	for index := 0; index < count; index++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			closeSessions(sessions)
			return nil, fmt.Errorf("open lookup session: %w", err)
		}
		statement, err := conn.PrepareContext(ctx, "SELECT id, payload FROM narrow_records WHERE id = ?")
		if err != nil {
			_ = conn.Close()
			closeSessions(sessions)
			return nil, fmt.Errorf("prepare primary-key lookup: %w", err)
		}
		sessions = append(sessions, workerSession{conn: conn, query: statement})
	}
	return sessions, nil
}

func closeSessions(sessions []workerSession) {
	for _, session := range sessions {
		if session.query != nil {
			_ = session.query.Close()
		}
		if session.conn != nil {
			_ = session.conn.Close()
		}
	}
}

func runLookups(sessions []workerSession, cfg config, ops int, collect bool) ([]time.Duration, int, int, string, error) {
	if ops == 0 {
		return nil, 0, 0, "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	body := strings.Repeat("x", cfg.payload)
	var wait sync.WaitGroup
	results := make(chan workerStats, len(sessions))
	for index, session := range sessions {
		wait.Add(1)
		go func(worker int, session workerSession) {
			defer wait.Done()
			results <- lookupWorker(ctx, session, cfg.rows, worker, ops, body, collect)
		}(index, session)
	}
	wait.Wait()
	close(results)
	var latencies []time.Duration
	work := 0
	failures := 0
	first := ""
	for stats := range results {
		work += stats.work
		failures += stats.errors
		latencies = append(latencies, stats.latencies...)
		if first == "" {
			first = stats.firstError
		}
	}
	if ctx.Err() != nil && failures == 0 && work != len(sessions)*ops {
		return latencies, work, failures, first, fmt.Errorf("lookup sample timed out: work=%d", work)
	}
	return latencies, work, failures, first, nil
}

type workerStats struct {
	latencies  []time.Duration
	work       int
	errors     int
	firstError string
}

func lookupWorker(ctx context.Context, session workerSession, rows, worker, ops int, body string, collect bool) workerStats {
	stats := workerStats{}
	if collect {
		stats.latencies = make([]time.Duration, 0, ops)
	}
	for sequence := 0; sequence < ops; sequence++ {
		if ctx.Err() != nil {
			stats.errors++
			if stats.firstError == "" {
				stats.firstError = ctx.Err().Error()
			}
			return stats
		}
		requestID := uniformRequestID(1, int64(worker), int64(sequence), int64(rows))
		started := time.Now()
		var id int64
		var payload string
		err := session.query.QueryRowContext(ctx, requestID).Scan(&id, &payload)
		elapsed := time.Since(started)
		if err != nil || id != requestID || payload != body {
			stats.errors++
			if stats.firstError == "" {
				if err != nil {
					stats.firstError = err.Error()
				} else {
					stats.firstError = fmt.Sprintf("row id=%d payload_len=%d want id=%d payload_len=%d", id, len(payload), requestID, len(body))
				}
			}
			return stats
		}
		stats.work++
		if collect {
			stats.latencies = append(stats.latencies, elapsed)
		}
	}
	return stats
}

func uniformRequestID(seed uint64, worker, sequence, rows int64) int64 {
	value := seed + uint64(worker)*0x9e3779b97f4a7c15 + uint64(sequence)*0xbf58476d1ce4e5b9
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	return int64(value%uint64(rows)) + 1
}

func medianDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func millis(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}
