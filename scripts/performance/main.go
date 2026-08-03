package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	scenarioVersion = "v0.1"
	corpusVersion   = "v0.1"
	password        = "performance-password"
	insertBase      = int64(1 << 61)
	warmupBase      = int64(1 << 60)
	maxPayloadBytes = 16384
)

type config struct {
	executable        string
	output            string
	dataRoot          string
	logicalBytes      int64
	narrowRows        int64
	relatedRows       int64
	sessions          int
	warmup            time.Duration
	run               time.Duration
	repetitions       int
	minimumOperations int64
	cleanStarts       int
	cleanStartLimit   time.Duration
	seed              uint64
	enforceThreshold  bool
}

type corpus struct {
	NarrowRows     int64  `json:"narrow_rows"`
	RelatedRows    int64  `json:"related_rows"`
	NarrowPayload  int    `json:"narrow_payload_bytes"`
	RelatedPayload int    `json:"related_payload_bytes"`
	NarrowExtra    int64  `json:"narrow_rows_with_extra_payload_byte"`
	RelatedExtra   int64  `json:"related_rows_with_extra_payload_byte"`
	LogicalBytes   int64  `json:"logical_bytes"`
	Seed           uint64 `json:"seed"`
}

type histogram struct {
	buckets []uint64
	total   uint64
}

const histogramBucket = 100 * time.Microsecond

func newHistogram() histogram {
	return histogram{buckets: make([]uint64, 600001)}
}

func (h *histogram) add(duration time.Duration) {
	index := int(duration / histogramBucket)
	if index >= len(h.buckets) {
		index = len(h.buckets) - 1
	}
	if index < 0 {
		index = 0
	}
	h.buckets[index]++
	h.total++
}

func (h histogram) percentile(fraction float64) time.Duration {
	if h.total == 0 {
		return 0
	}
	rank := uint64(math.Ceil(fraction * float64(h.total)))
	if rank == 0 {
		rank = 1
	}
	var seen uint64
	for index, count := range h.buckets {
		seen += count
		if seen >= rank {
			return time.Duration(index+1) * histogramBucket
		}
	}
	return time.Duration(len(h.buckets)) * histogramBucket
}

func (h *histogram) merge(other histogram) {
	if len(h.buckets) == 0 {
		*h = newHistogram()
	}
	for index, count := range other.buckets {
		h.buckets[index] += count
	}
	h.total += other.total
}

type phaseStats struct {
	operations uint64
	errors     uint64
	unfinished uint64
	elapsed    time.Duration
	latencies  histogram
	firstError string
}

type runEvidence struct {
	Run         int     `json:"run"`
	Valid       bool    `json:"valid"`
	Passed      bool    `json:"passed"`
	Operations  uint64  `json:"successful_operations"`
	Errors      uint64  `json:"errors"`
	Unfinished  uint64  `json:"unfinished_operations"`
	Throughput  float64 `json:"successful_operations_per_second"`
	P50Millis   float64 `json:"p50_ms"`
	P95Millis   float64 `json:"p95_ms"`
	P99Millis   float64 `json:"p99_ms"`
	ElapsedSecs float64 `json:"elapsed_seconds"`
	Failure     string  `json:"failure,omitempty"`
}

type gateEvidence struct {
	Name          string        `json:"name"`
	Operation     string        `json:"operation"`
	P95LimitMS    float64       `json:"p95_limit_ms"`
	P99LimitMS    float64       `json:"p99_limit_ms"`
	ThroughputMin float64       `json:"throughput_minimum_per_second"`
	Runs          []runEvidence `json:"runs"`
	PassingRuns   int           `json:"passing_runs"`
	ValidRuns     int           `json:"valid_runs"`
	Passed        bool          `json:"passed"`
}

type cleanStartEvidence struct {
	LimitMS   float64   `json:"limit_ms"`
	Durations []float64 `json:"durations_ms"`
	Passing   int       `json:"passing_starts"`
	Required  int       `json:"required_passing_starts"`
	Passed    bool      `json:"passed"`
}

type evidence struct {
	Schema            string             `json:"schema"`
	ScenarioVersion   string             `json:"scenario_version"`
	CorpusVersion     string             `json:"corpus_version"`
	StartedAt         time.Time          `json:"started_at"`
	FinishedAt        time.Time          `json:"finished_at"`
	Environment       map[string]string  `json:"environment"`
	Configuration     map[string]any     `json:"configuration"`
	Corpus            corpus             `json:"corpus"`
	Gates             []gateEvidence     `json:"gates"`
	CleanStarts       cleanStartEvidence `json:"clean_starts"`
	AcceptanceEnabled bool               `json:"acceptance_enabled"`
	Judgment          string             `json:"judgment"`
}

type runningServer struct {
	command  *exec.Cmd
	address  string
	stderr   strings.Builder
	readDone sync.WaitGroup
}

type workerSession struct {
	conn  *sql.Conn
	query *sql.Stmt
}

type gateDefinition struct {
	name          string
	operation     string
	p95Limit      time.Duration
	p99Limit      time.Duration
	throughputMin float64
}

var gateDefinitions = []gateDefinition{
	{name: "primary_key_lookup", operation: "Look up one narrow keyed record by its primary key", p95Limit: 10 * time.Millisecond, p99Limit: 40 * time.Millisecond, throughputMin: 5000},
	{name: "unique_key_lookup", operation: "Look up one narrow keyed record by its unique key", p95Limit: 10 * time.Millisecond, p99Limit: 40 * time.Millisecond, throughputMin: 5000},
	{name: "durable_insert", operation: "Insert one row and commit it as its own durable transaction", p95Limit: 25 * time.Millisecond, p99Limit: 100 * time.Millisecond, throughputMin: 500},
	{name: "indexed_update", operation: "Update one row and commit an indexed value as its own durable transaction", p95Limit: 25 * time.Millisecond, p99Limit: 100 * time.Millisecond, throughputMin: 500},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.New(os.Stderr, "performance: ", 0).Fatal(err)
	}
}

func run(args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	if err := prepareRun(cfg); err != nil {
		return err
	}
	result := newEvidence(cfg)
	dataDirectory, err := os.MkdirTemp(cfg.dataRoot, "database-performance-")
	if err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	defer os.RemoveAll(dataDirectory)
	return executeRun(cfg, dataDirectory, &result)
}

func prepareRun(cfg config) error {
	if _, err := os.Stat(cfg.executable); err != nil {
		return fmt.Errorf("executable %q: %w", cfg.executable, err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.output), 0o755); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	return nil
}

func executeRun(cfg config, directory string, result *evidence) error {
	if err := initialize(cfg.executable, directory); err != nil {
		return err
	}
	server, db, loadedCorpus, err := loadAndRestart(cfg, directory)
	if err != nil {
		return err
	}
	result.Corpus = loadedCorpus
	if err := runGates(db, loadedCorpus, cfg, result); err != nil {
		return closeMeasurement(server, db, err)
	}
	if err := closeMeasurement(server, db, nil); err != nil {
		return err
	}
	return finishEvidence(cfg, directory, result)
}

func closeMeasurement(server *runningServer, db *sql.DB, cause error) error {
	dbErr := db.Close()
	serverErr := server.stop()
	if cause != nil {
		return cause
	}
	if dbErr != nil {
		return fmt.Errorf("close gate connection: %w", dbErr)
	}
	return serverErr
}

func finishEvidence(cfg config, directory string, result *evidence) error {
	clean, err := measureCleanStarts(cfg, directory)
	if err != nil {
		return err
	}
	result.CleanStarts = clean
	result.FinishedAt = time.Now().UTC()
	rawJudgment := judge(*result)
	result.Judgment = rawJudgment
	if !acceptanceEnabled(cfg, result.Environment) {
		result.Judgment = "diagnostic_only"
	}
	if err := writeEvidence(cfg.output, *result); err != nil {
		return err
	}
	fmt.Printf("performance evidence written to %s\n", cfg.output)
	if cfg.enforceThreshold && rawJudgment != "accepted" {
		return errors.New("performance acceptance thresholds were not met")
	}
	return nil
}

func acceptanceEnabled(cfg config, environment map[string]string) bool {
	return cfg.enforceThreshold && cfg.dataRoot == "" && fixedAcceptanceContract(cfg) && isReferenceEnvironment(environment)
}

func fixedAcceptanceContract(cfg config) bool {
	return cfgMatchesAcceptanceSize(cfg) && cfgMatchesAcceptanceTiming(cfg) && cfg.seed == 0x9e3779b97f4a7c15
}

func cfgMatchesAcceptanceSize(cfg config) bool {
	return cfg.logicalBytes == 10_000_000_000 &&
		cfg.narrowRows == 1_000_000 &&
		cfg.relatedRows == 1_000_000 &&
		cfg.sessions == 50 &&
		cfg.minimumOperations == 100_000 &&
		cfg.cleanStarts == 10
}

func cfgMatchesAcceptanceTiming(cfg config) bool {
	return cfg.warmup == 5*time.Minute &&
		cfg.run == 5*time.Minute &&
		cfg.repetitions == 5 &&
		cfg.cleanStartLimit == 3*time.Second
}

func isReferenceEnvironment(environment map[string]string) bool {
	if environment["goos"] != "darwin" || environment["machine_model"] != "Mac15,5" {
		return false
	}
	if !strings.Contains(environment["cpu_model"], "Apple M3") {
		return false
	}
	memory, err := strconv.ParseUint(environment["memory_bytes"], 10, 64)
	if err != nil {
		return false
	}
	return memory >= 16<<30
}

func newEvidence(cfg config) evidence {
	environment := benchmarkEnvironment(cfg.executable)
	return evidence{
		Schema:          "database.performance/v1",
		ScenarioVersion: scenarioVersion,
		CorpusVersion:   corpusVersion,
		StartedAt:       time.Now().UTC(),
		Environment:     environment,
		Configuration: map[string]any{
			"logical_bytes_target": cfg.logicalBytes,
			"sessions":             cfg.sessions,
			"warmup_seconds":       cfg.warmup.Seconds(),
			"run_seconds":          cfg.run.Seconds(),
			"repetitions":          cfg.repetitions,
			"minimum_operations":   cfg.minimumOperations,
			"clean_starts":         cfg.cleanStarts,
			"seed":                 cfg.seed,
			"data_root":            cfg.dataRoot,
		},
		AcceptanceEnabled: acceptanceEnabled(cfg, environment),
	}
}

type systemCommand struct {
	key     string
	command string
	args    []string
}

func benchmarkEnvironment(executable string) map[string]string {
	environment := collectHostEnvironment()
	environment["go_version"] = runtime.Version()
	environment["driver"] = "github.com/go-sql-driver/mysql v1.9.3"
	environment["server"] = versionInfo(executable)
	environment["reference_environment"] = strconv.FormatBool(isReferenceEnvironment(environment))
	return environment
}

func collectHostEnvironment() map[string]string {
	environment := map[string]string{
		"goos":          runtime.GOOS,
		"goarch":        runtime.GOARCH,
		"os_version":    "unknown",
		"kernel":        runSystemCommand("uname", "-r"),
		"cpu_model":     "unknown",
		"cpu_count":     "unknown",
		"memory_bytes":  "unknown",
		"machine_model": "unknown",
	}
	switch runtime.GOOS {
	case "darwin":
		environment["os_version"] = runSystemCommand("sw_vers", "-productVersion")
		environment["cpu_model"] = runSystemCommand("sysctl", "-n", "machdep.cpu.brand_string")
		environment["cpu_count"] = runSystemCommand("sysctl", "-n", "hw.ncpu")
		environment["memory_bytes"] = runSystemCommand("sysctl", "-n", "hw.memsize")
		environment["machine_model"] = runSystemCommand("sysctl", "-n", "hw.model")
	case "linux":
		environment["os_version"] = runSystemCommand("uname", "-sr")
		environment["cpu_model"] = linuxCPUModel()
		environment["cpu_count"] = strconv.Itoa(runtime.NumCPU())
		environment["memory_bytes"] = linuxMemoryBytes()
		environment["machine_model"] = linuxMachineModel()
	default:
		environment["os_version"] = runSystemCommand("uname", "-sr")
	}
	return environment
}

func linuxCPUModel() string {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}

func linuxMemoryBytes() string {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "unknown"
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return "unknown"
		}
		return strconv.FormatUint(kilobytes*1024, 10)
	}
	return "unknown"
}

func linuxMachineModel() string {
	candidates := []string{
		"/sys/devices/virtual/dmi/id/product_name",
		"/sys/firmware/devicetree/base/model",
	}
	for _, path := range candidates {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(content))
		if value != "" {
			return value
		}
	}
	return "unknown"
}

func runSystemCommand(command string, args ...string) string {
	output, err := exec.Command(command, args...).Output()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func loadAndRestart(cfg config, directory string) (*runningServer, *sql.DB, corpus, error) {
	loaded, err := loadCorpusAndStop(cfg, directory)
	if err != nil {
		return nil, nil, corpus{}, err
	}
	server, db, err := restartForMeasurement(cfg, directory)
	if err != nil {
		return nil, nil, corpus{}, err
	}
	fmt.Println("clean corpus server restarted")
	return server, db, loaded, nil
}

func loadCorpusAndStop(cfg config, directory string) (corpus, error) {
	server, db, startup, err := startServer(cfg.executable, directory)
	if err != nil {
		return corpus{}, err
	}
	fmt.Printf("server ready in %.3fs\n", startup.Seconds())
	loaded, err := loadInitialCorpus(db, cfg)
	if err != nil {
		_ = db.Close()
		_ = server.stop()
		return corpus{}, err
	}
	if err := db.Close(); err != nil {
		_ = server.stop()
		return corpus{}, fmt.Errorf("close load connection: %w", err)
	}
	if err := server.stop(); err != nil {
		return corpus{}, err
	}
	return loaded, nil
}

func restartForMeasurement(cfg config, directory string) (*runningServer, *sql.DB, error) {
	server, probe, _, err := startServer(cfg.executable, directory)
	if err != nil {
		return nil, nil, err
	}
	if err := probe.Close(); err != nil {
		_ = server.stop()
		return nil, nil, fmt.Errorf("close restart probe connection: %w", err)
	}
	db, err := openPerformanceDB(server.address)
	if err != nil {
		_ = server.stop()
		return nil, nil, fmt.Errorf("open performance database after restart: %w", err)
	}
	db.SetMaxOpenConns(cfg.sessions)
	db.SetMaxIdleConns(cfg.sessions)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = server.stop()
		return nil, nil, fmt.Errorf("ping performance database after restart: %w", err)
	}
	return server, db, nil
}

func loadInitialCorpus(db *sql.DB, cfg config) (corpus, error) {
	if err := createSchema(db); err != nil {
		return corpus{}, err
	}
	return loadCorpus(db, cfg)
}

func runGates(db *sql.DB, c corpus, cfg config, result *evidence) error {
	for _, definition := range gateDefinitions {
		gate, err := runGate(db, c, cfg, definition)
		if err != nil {
			return err
		}
		result.Gates = append(result.Gates, gate)
		fmt.Printf("%s: %d/%d measured runs passed\n", definition.name, gate.PassingRuns, gate.ValidRuns)
	}
	return nil
}

func parseConfig(args []string) (config, error) {
	root, err := os.Getwd()
	if err != nil {
		return config{}, err
	}
	cfg := config{}
	flags := flag.NewFlagSet("performance", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&cfg.executable, "executable", filepath.Join(root, "bin", "database"), "database executable")
	flags.StringVar(&cfg.output, "output", filepath.Join(root, "dist", "performance-evidence.json"), "evidence JSON path")
	flags.StringVar(&cfg.dataRoot, "data-root", "", "directory for temporary benchmark data")
	flags.Int64Var(&cfg.logicalBytes, "logical-bytes", 10_000_000_000, "logical application bytes in the corpus")
	flags.Int64Var(&cfg.narrowRows, "narrow-rows", 1_000_000, "narrow keyed record count")
	flags.Int64Var(&cfg.relatedRows, "related-rows", 1_000_000, "larger related record count")
	flags.IntVar(&cfg.sessions, "sessions", 50, "simultaneous authenticated sessions")
	flags.DurationVar(&cfg.warmup, "warmup", 5*time.Minute, "unmeasured warm-up per gate")
	flags.DurationVar(&cfg.run, "run", 5*time.Minute, "measured duration per run")
	flags.IntVar(&cfg.repetitions, "repetitions", 5, "measured runs per gate")
	flags.Int64Var(&cfg.minimumOperations, "minimum-operations", 100_000, "minimum successful operations per run")
	flags.IntVar(&cfg.cleanStarts, "clean-starts", 10, "number of clean-start measurements")
	flags.DurationVar(&cfg.cleanStartLimit, "clean-start-limit", 3*time.Second, "clean-start threshold")
	flags.Uint64Var(&cfg.seed, "seed", 0x9e3779b97f4a7c15, "deterministic corpus and request seed")
	flags.BoolVar(&cfg.enforceThreshold, "enforce-thresholds", true, "fail unless the fixed acceptance thresholds pass")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return cfg, validateConfig(cfg)
}

func validateConfig(cfg config) error {
	positive := []int64{cfg.logicalBytes, cfg.narrowRows, cfg.relatedRows, int64(cfg.sessions), int64(cfg.warmup), int64(cfg.run), int64(cfg.repetitions), cfg.minimumOperations, int64(cfg.cleanStarts), int64(cfg.cleanStartLimit)}
	for _, value := range positive {
		if value <= 0 {
			return errors.New("all performance limits must be positive")
		}
	}
	if cfg.sessions > 100 {
		return errors.New("sessions cannot exceed the default server connection ceiling")
	}
	return nil
}

func versionInfo(executable string) string {
	output, err := exec.Command(executable, "version", "--format=json").Output()
	if err != nil {
		return "unknown"
	}
	var info struct {
		ProductVersion string `json:"product_version"`
		BuildIdentity  string `json:"build_identity"`
		Platform       string `json:"platform"`
	}
	if json.Unmarshal(output, &info) != nil {
		return "unknown"
	}
	return fmt.Sprintf("%s (%s, %s)", info.ProductVersion, info.BuildIdentity, info.Platform)
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

func startServer(executable, directory string) (*runningServer, *sql.DB, time.Duration, error) {
	address, err := freeAddress()
	if err != nil {
		return nil, nil, 0, err
	}
	command := exec.Command(executable, "serve", "--data-directory", directory, "--mysql-listen-address", address, "--format=json")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, 0, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, nil, 0, err
	}
	if err := command.Start(); err != nil {
		return nil, nil, 0, fmt.Errorf("start server: %w", err)
	}
	started := time.Now()
	server := &runningServer{command: command, address: address}
	server.readDone.Add(2)
	go func() {
		defer server.readDone.Done()
		_, _ = ioCopyDiscard(stdout)
	}()
	go func() {
		defer server.readDone.Done()
		_, _ = ioCopyBuilder(&server.stderr, stderr)
	}()
	db, err := openRootDB(address)
	if err != nil {
		_ = server.stop()
		return nil, nil, 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		if err := db.PingContext(ctx); err == nil {
			return server, db, time.Since(started), nil
		}
		if ctx.Err() != nil {
			_ = db.Close()
			serverErr := server.stop()
			return nil, nil, 0, fmt.Errorf("server did not become ready: %w (server stop: %v)", ctx.Err(), serverErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (server *runningServer) stop() error {
	if server == nil || server.command == nil || server.command.Process == nil {
		return nil
	}
	if err := server.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop server: %w", err)
	}
	err := server.command.Wait()
	server.readDone.Wait()
	if err != nil {
		return fmt.Errorf("wait for server: %w; stderr=%s", err, strings.TrimSpace(server.stderr.String()))
	}
	return nil
}

func ioCopyDiscard(reader interface{ Read([]byte) (int, error) }) (int64, error) {
	buffer := make([]byte, 4096)
	var total int64
	for {
		count, err := reader.Read(buffer)
		total += int64(count)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return total, nil
			}
			return total, err
		}
	}
}

func ioCopyBuilder(builder *strings.Builder, reader interface{ Read([]byte) (int, error) }) (int64, error) {
	buffer := make([]byte, 4096)
	var total int64
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			builder.Write(buffer[:count])
			total += int64(count)
		}
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return total, nil
			}
			return total, err
		}
	}
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

func dsn(address, database string) string {
	configuration := mysql.Config{User: "admin", Passwd: password, Net: "tcp", Addr: address, DBName: database, ParseTime: true, MaxAllowedPacket: 64 << 20, Timeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	return configuration.FormatDSN()
}

func openRootDB(address string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn(address, ""))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(50)
	return db, nil
}

func openPerformanceDB(address string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn(address, "performance"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(50)
	return db, nil
}

func createSchema(root *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := root.ExecContext(ctx, "CREATE DATABASE performance"); err != nil {
		return fmt.Errorf("create performance database: %w", err)
	}
	if _, err := root.ExecContext(ctx, "USE performance"); err != nil {
		return fmt.Errorf("use performance database: %w", err)
	}
	statements := []string{
		`CREATE TABLE narrow_records (
			id BIGINT NOT NULL,
			unique_key VARCHAR(64) NOT NULL,
			secondary_key VARCHAR(64) NOT NULL,
			base_secondary_key VARCHAR(64) NOT NULL,
			numeric_value BIGINT NOT NULL,
			payload VARCHAR(16384) NOT NULL,
			PRIMARY KEY (id),
			UNIQUE KEY unique_narrow_key (unique_key),
			KEY secondary_narrow_key (secondary_key)
		)`,
		`CREATE TABLE related_records (
			id BIGINT NOT NULL,
			parent_id BIGINT NOT NULL,
			secondary_key VARCHAR(64) NOT NULL,
			observed_at DATETIME NOT NULL,
			numeric_value BIGINT NOT NULL,
			payload VARCHAR(16384) NOT NULL,
			PRIMARY KEY (id),
			KEY parent_related_key (parent_id),
			KEY secondary_related_key (secondary_key)
		)`,
		`CREATE TABLE benchmark_inserts (
			id BIGINT NOT NULL,
			parent_id BIGINT NOT NULL,
			secondary_key VARCHAR(64) NOT NULL,
			observed_at DATETIME NOT NULL,
			numeric_value BIGINT NOT NULL,
			payload VARCHAR(256) NOT NULL,
			PRIMARY KEY (id)
		)`,
	}
	for _, statement := range statements {
		if _, err := root.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create corpus table: %w", err)
		}
	}
	return nil
}

func loadCorpus(db *sql.DB, cfg config) (corpus, error) {
	loaded := corpus{NarrowRows: cfg.narrowRows, RelatedRows: cfg.relatedRows, Seed: cfg.seed}
	payloads, err := derivePayloadWidths(cfg)
	if err != nil {
		return corpus{}, err
	}
	loaded.NarrowPayload = payloads.narrow
	loaded.RelatedPayload = payloads.related
	loaded.NarrowExtra = payloads.narrowExtra
	loaded.RelatedExtra = payloads.relatedExtra
	loaded.LogicalBytes = payloads.logicalBytes
	ctx := context.Background()
	if err := loadNarrow(ctx, db, loaded); err != nil {
		return corpus{}, err
	}
	if err := loadRelated(ctx, db, loaded); err != nil {
		return corpus{}, err
	}
	fmt.Printf("corpus loaded: %d narrow rows, %d related rows, %d logical bytes\n", loaded.NarrowRows, loaded.RelatedRows, loaded.LogicalBytes)
	return loaded, nil
}

type payloadWidths struct {
	narrow, related int
	narrowExtra     int64
	relatedExtra    int64
	logicalBytes    int64
}

func derivePayloadWidths(cfg config) (payloadWidths, error) {
	rows := cfg.narrowRows + cfg.relatedRows
	fixed := corpusFixedBytes(cfg)
	if cfg.logicalBytes <= fixed {
		return payloadWidths{}, fmt.Errorf("logical byte target %d is not larger than fixed corpus values %d", cfg.logicalBytes, fixed)
	}
	payloadBytes := cfg.logicalBytes - fixed
	perRow := payloadBytes / rows
	if perRow <= 0 || perRow > maxPayloadBytes || (perRow == maxPayloadBytes && payloadBytes%rows != 0) {
		return payloadWidths{}, fmt.Errorf("derived payload width %d is outside supported range", perRow)
	}
	narrowExtra, relatedExtra, err := splitPayloadRemainder(payloadBytes-perRow*rows, cfg.narrowRows, cfg.relatedRows)
	if err != nil {
		return payloadWidths{}, err
	}
	return payloadWidths{narrow: int(perRow), related: int(perRow), narrowExtra: narrowExtra, relatedExtra: relatedExtra, logicalBytes: cfg.logicalBytes}, nil
}

func corpusFixedBytes(cfg config) int64 {
	var fixed int64
	for index := int64(1); index <= cfg.narrowRows; index++ {
		fixed += narrowLogicalBytes(index, "")
	}
	for index := int64(1); index <= cfg.relatedRows; index++ {
		fixed += relatedLogicalBytes(index, "")
	}
	return fixed
}

func splitPayloadRemainder(remainder, narrowRows, relatedRows int64) (int64, int64, error) {
	narrowExtra := remainder
	if narrowExtra > narrowRows {
		narrowExtra = narrowRows
	}
	relatedExtra := remainder - narrowExtra
	if relatedExtra > relatedRows {
		return 0, 0, fmt.Errorf("payload remainder %d exceeds the configured row count", remainder)
	}
	return narrowExtra, relatedExtra, nil
}

func narrowLogicalBytes(id int64, payload string) int64 {
	unique := fmt.Sprintf("unique-%d", id)
	secondary := fmt.Sprintf("secondary-%d", id%10000)
	return int64(len(strconv.FormatInt(id, 10)) + len(unique) + len(secondary) + len(secondary) + len("0") + len(payload))
}

func relatedLogicalBytes(id int64, payload string) int64 {
	parent := (id-1)%1000000 + 1
	secondary := fmt.Sprintf("related-%d", id%10000)
	return int64(len(strconv.FormatInt(id, 10)) + len(strconv.FormatInt(parent, 10)) + len(secondary) + len("2024-01-01 00:00:00") + len("0") + len(payload))
}

func payload(width int, extra bool) string {
	if extra {
		return strings.Repeat("x", width+1)
	}
	return strings.Repeat("x", width)
}

func loadNarrow(ctx context.Context, db *sql.DB, c corpus) error {
	statement, err := db.PrepareContext(ctx, "INSERT INTO narrow_records (id, unique_key, secondary_key, base_secondary_key, numeric_value, payload) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare narrow loader: %w", err)
	}
	defer statement.Close()
	return loadRows(ctx, db, statement, c.NarrowRows, c.NarrowPayload, func(index int64, width int) []any {
		secondary := fmt.Sprintf("secondary-%d", index%10000)
		return []any{index, fmt.Sprintf("unique-%d", index), secondary, secondary, 0, payload(width, index <= c.NarrowExtra)}
	}, "narrow")
}

func loadRelated(ctx context.Context, db *sql.DB, c corpus) error {
	statement, err := db.PrepareContext(ctx, "INSERT INTO related_records (id, parent_id, secondary_key, observed_at, numeric_value, payload) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare related loader: %w", err)
	}
	defer statement.Close()
	return loadRows(ctx, db, statement, c.RelatedRows, c.RelatedPayload, func(index int64, width int) []any {
		parent := (index-1)%c.NarrowRows + 1
		return []any{index, parent, fmt.Sprintf("related-%d", index%10000), "2024-01-01 00:00:00", 0, payload(width, index <= c.RelatedExtra)}
	}, "related")
}

func loadRows(ctx context.Context, db *sql.DB, statement *sql.Stmt, rows int64, width int, values func(int64, int) []any, name string) error {
	const batchSize = 500
	for start := int64(1); start <= rows; start += batchSize {
		end := start + batchSize
		if end > rows+1 {
			end = rows + 1
		}
		if err := loadBatch(ctx, db, statement, start, end, width, values, name); err != nil {
			return err
		}
		if shouldReportBatch(start, end, rows, batchSize) {
			fmt.Printf("loaded %s rows through %d/%d\n", name, end-1, rows)
		}
	}
	return nil
}

func loadBatch(ctx context.Context, db *sql.DB, statement *sql.Stmt, start, end int64, width int, values func(int64, int) []any, name string) error {
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s load transaction: %w", name, err)
	}
	for index := start; index < end; index++ {
		if _, err := transaction.StmtContext(ctx, statement).ExecContext(ctx, values(index, width)...); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("load %s row %d: %w", name, index, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit %s load transaction: %w", name, err)
	}
	return nil
}

func shouldReportBatch(start, end, rows int64, batchSize int64) bool {
	return start == 1 || end == rows+1 || start%(batchSize*100) == 1
}

func runGate(db *sql.DB, c corpus, cfg config, definition gateDefinition) (gateEvidence, error) {
	result := gateEvidence{Name: definition.name, Operation: definition.operation, P95LimitMS: definition.p95Limit.Seconds() * 1000, P99LimitMS: definition.p99Limit.Seconds() * 1000, ThroughputMin: definition.throughputMin}
	if err := runGateWarmup(db, c, cfg, definition); err != nil {
		return result, err
	}
	if err := runGateRepetitions(db, c, cfg, definition, &result); err != nil {
		return result, err
	}
	if err := restoreCorpus(db); err != nil {
		return result, err
	}
	result.Passed = result.PassingRuns >= 4 && result.ValidRuns == cfg.repetitions
	return result, nil
}

func runGateWarmup(db *sql.DB, c corpus, cfg config, definition gateDefinition) error {
	fmt.Printf("%s warm-up\n", definition.name)
	warmup, err := executeGate(db, c, cfg, definition, false, 0)
	if err != nil {
		return err
	}
	if warmup.firstError != "" {
		return fmt.Errorf("%s warm-up failed: %s", definition.name, warmup.firstError)
	}
	return restoreCorpus(db)
}

func runGateRepetitions(db *sql.DB, c corpus, cfg config, definition gateDefinition, result *gateEvidence) error {
	for repetition := 1; repetition <= cfg.repetitions; repetition++ {
		if repetition > 1 {
			if err := restoreCorpus(db); err != nil {
				return err
			}
		}
		run, err := runGateRepetition(db, c, cfg, definition, repetition)
		if err != nil {
			return err
		}
		result.Runs = append(result.Runs, run)
		result.ValidRuns++
		if run.Passed {
			result.PassingRuns++
		}
		fmt.Printf("%s run %d: %.1f/s p95 %.3fms p99 %.3fms (%s)\n", definition.name, repetition, run.Throughput, run.P95Millis, run.P99Millis, map[bool]string{true: "pass", false: "fail"}[run.Passed])
	}
	return nil
}

func runGateRepetition(db *sql.DB, c corpus, cfg config, definition gateDefinition, repetition int) (runEvidence, error) {
	fmt.Printf("%s run %d/%d measure\n", definition.name, repetition, cfg.repetitions)
	stats, err := executeGate(db, c, cfg, definition, true, repetition)
	if err != nil {
		return runEvidence{}, err
	}
	return makeRunEvidence(stats, cfg, definition, repetition), nil
}

func makeRunEvidence(stats phaseStats, cfg config, definition gateDefinition, repetition int) runEvidence {
	run := runEvidence{Run: repetition, Valid: true, Operations: stats.operations, Errors: stats.errors, Unfinished: stats.unfinished, ElapsedSecs: stats.elapsed.Seconds(), Throughput: float64(stats.operations) / stats.elapsed.Seconds(), P50Millis: stats.latencies.percentile(0.50).Seconds() * 1000, P95Millis: stats.latencies.percentile(0.95).Seconds() * 1000, P99Millis: stats.latencies.percentile(0.99).Seconds() * 1000}
	run.Passed = runMeetsThresholds(run, cfg.minimumOperations, definition)
	if !run.Passed {
		run.Failure = fmt.Sprintf("operations=%d errors=%d unfinished=%d p95=%.3fms p99=%.3fms throughput=%.1f/s", run.Operations, run.Errors, run.Unfinished, run.P95Millis, run.P99Millis, run.Throughput)
		if stats.firstError != "" {
			run.Failure += ": " + stats.firstError
		}
	}
	return run
}

func runMeetsThresholds(run runEvidence, minimumOperations int64, definition gateDefinition) bool {
	return run.Operations >= uint64(minimumOperations) && run.Errors == 0 && run.P95Millis <= definition.p95Limit.Seconds()*1000 && run.P99Millis <= definition.p99Limit.Seconds()*1000 && run.Throughput >= definition.throughputMin
}

func executeGate(db *sql.DB, c corpus, cfg config, definition gateDefinition, measured bool, repetition int) (phaseStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.warmup+cfg.run+2*time.Minute)
	defer cancel()
	sessions, err := prepareSessions(ctx, db, definition, cfg.sessions)
	if err != nil {
		return phaseStats{}, err
	}
	defer closeSessions(sessions)
	base := warmupBase
	if measured {
		base = insertBase + int64(repetition)*1_000_000_000
	}
	if !measured {
		if _, err := runPhase(ctx, sessions, cfg.warmup, definition, c, cfg, base, false); err != nil {
			return phaseStats{}, err
		}
		return phaseStats{}, nil
	}
	return runPhase(ctx, sessions, cfg.run, definition, c, cfg, base, true)
}

func prepareSessions(ctx context.Context, db *sql.DB, definition gateDefinition, maxSessions int) ([]workerSession, error) {
	query := ""
	switch definition.name {
	case "primary_key_lookup":
		query = "SELECT id, payload FROM narrow_records WHERE id = ?"
	case "unique_key_lookup":
		query = "SELECT id, payload FROM narrow_records WHERE unique_key = ?"
	case "durable_insert":
		query = "INSERT INTO benchmark_inserts (id, parent_id, secondary_key, observed_at, numeric_value, payload) VALUES (?, ?, ?, ?, ?, ?)"
	case "indexed_update":
		query = "UPDATE narrow_records SET numeric_value = ?, secondary_key = ? WHERE id = ?"
	}
	sessions := make([]workerSession, 0, maxSessions)
	for index := 0; index < maxSessions; index++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			closeSessions(sessions)
			return nil, fmt.Errorf("open performance session: %w", err)
		}
		if err := conn.PingContext(ctx); err != nil {
			_ = conn.Close()
			closeSessions(sessions)
			return nil, fmt.Errorf("authenticate performance session: %w", err)
		}
		statement, err := conn.PrepareContext(ctx, query)
		if err != nil {
			_ = conn.Close()
			closeSessions(sessions)
			return nil, fmt.Errorf("prepare %s: %w", definition.name, err)
		}
		sessions = append(sessions, workerSession{conn: conn, query: statement})
	}
	return sessions, nil
}

func closeSessions(sessions []workerSession) {
	for _, session := range sessions {
		_ = session.query.Close()
		_ = session.conn.Close()
	}
}

func runPhase(parent context.Context, sessions []workerSession, duration time.Duration, definition gateDefinition, c corpus, cfg config, base int64, collect bool) (phaseStats, error) {
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	start := make(chan struct{})
	phaseStarted := time.Now()
	results := make(chan phaseStats, len(sessions))
	var wait sync.WaitGroup
	for index, session := range sessions {
		wait.Add(1)
		go func(worker int, session workerSession) {
			defer wait.Done()
			results <- runWorker(ctx, start, session, definition, c, base, int64(worker), collect)
		}(index, session)
	}
	close(start)
	wait.Wait()
	return combinePhase(results, len(sessions), time.Since(phaseStarted)), nil
}

func runWorker(ctx context.Context, start <-chan struct{}, session workerSession, definition gateDefinition, c corpus, base, worker int64, collect bool) phaseStats {
	local := phaseStats{latencies: newHistogram()}
	<-start
	for sequence := int64(0); ; sequence++ {
		if ctx.Err() != nil {
			return local
		}
		started := time.Now()
		err := executeOperation(ctx, session, definition.name, c.Seed, c.NarrowRows, base, worker, sequence)
		elapsed := time.Since(started)
		if err != nil {
			return recordWorkerError(local, ctx, err)
		}
		if collect {
			local.operations++
			local.latencies.add(elapsed)
		}
	}
}

func recordWorkerError(stats phaseStats, ctx context.Context, err error) phaseStats {
	if ctx.Err() != nil {
		stats.unfinished++
		return stats
	}
	stats.errors++
	stats.firstError = err.Error()
	return stats
}

func combinePhase(results <-chan phaseStats, count int, elapsed time.Duration) phaseStats {
	combined := phaseStats{latencies: newHistogram(), elapsed: elapsed}
	for index := 0; index < count; index++ {
		local := <-results
		combined.operations += local.operations
		combined.errors += local.errors
		combined.unfinished += local.unfinished
		combined.latencies.merge(local.latencies)
		if combined.firstError == "" {
			combined.firstError = local.firstError
		}
	}
	return combined
}

func executeOperation(ctx context.Context, session workerSession, operation string, seed uint64, rows, base, worker, sequence int64) error {
	key := base + worker + sequence*100
	requestID := uniformRequestID(seed, worker, sequence, rows)
	switch operation {
	case "primary_key_lookup":
		var id int64
		var value string
		return session.query.QueryRowContext(ctx, requestID).Scan(&id, &value)
	case "unique_key_lookup":
		var id int64
		var value string
		return session.query.QueryRowContext(ctx, fmt.Sprintf("unique-%d", requestID)).Scan(&id, &value)
	case "durable_insert":
		_, err := session.query.ExecContext(ctx, key, requestID, fmt.Sprintf("insert-%d", key), "2024-01-01 00:00:00", sequence, "performance-insert")
		return err
	case "indexed_update":
		_, err := session.query.ExecContext(ctx, sequence, fmt.Sprintf("measured-%d", key%10000), requestID)
		return err
	default:
		return fmt.Errorf("unknown operation %q", operation)
	}
}

func uniformRequestID(seed uint64, worker, sequence, rows int64) int64 {
	value := seed + uint64(worker)*0x9e3779b97f4a7c15 + uint64(sequence)*0xbf58476d1ce4e5b9
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	return int64(value%uint64(rows)) + 1
}

func restoreCorpus(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, "DELETE FROM benchmark_inserts"); err != nil {
		return fmt.Errorf("restore inserted rows: %w", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE narrow_records SET numeric_value = 0, secondary_key = base_secondary_key"); err != nil {
		return fmt.Errorf("restore updated rows: %w", err)
	}
	return nil
}

func measureCleanStarts(cfg config, directory string) (cleanStartEvidence, error) {
	evidence := cleanStartEvidence{LimitMS: cfg.cleanStartLimit.Seconds() * 1000, Required: cfg.cleanStarts - 1}
	for index := 0; index < cfg.cleanStarts; index++ {
		server, db, duration, err := startServer(cfg.executable, directory)
		if err != nil {
			return evidence, err
		}
		evidence.Durations = append(evidence.Durations, duration.Seconds()*1000)
		if duration <= cfg.cleanStartLimit {
			evidence.Passing++
		}
		_ = db.Close()
		if err := server.stop(); err != nil {
			return evidence, err
		}
		fmt.Printf("clean start %d/%d: %.3fms\n", index+1, cfg.cleanStarts, duration.Seconds()*1000)
	}
	evidence.Passed = evidence.Passing >= evidence.Required
	return evidence, nil
}

func judge(result evidence) string {
	if len(result.Gates) != len(gateDefinitions) {
		return "failed"
	}
	for _, gate := range result.Gates {
		if !gate.Passed {
			return "failed"
		}
	}
	if !result.CleanStarts.Passed {
		return "failed"
	}
	return "accepted"
}

func writeEvidence(path string, result evidence) error {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write evidence: %w", err)
	}
	return nil
}
