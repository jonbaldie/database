package blackbox_test

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/test/blackbox"
)

type ActionType string

const (
	ActInit               ActionType = "init"
	ActConfigValidate     ActionType = "config_validate"
	ActDataValidate       ActionType = "data_validate"
	ActDataInspect        ActionType = "data_inspect"
	ActServeStart         ActionType = "serve_start"
	ActSQL                ActionType = "sql"
	ActBackupCreate       ActionType = "backup_create"
	ActBackupInspect      ActionType = "backup_inspect"
	ActShutdownCLI        ActionType = "shutdown_cli"
	ActShutdownSIGTERM    ActionType = "shutdown_sigterm"
	ActKillServer         ActionType = "kill_server"
	ActRestore            ActionType = "restore"
	ActUpgrade            ActionType = "upgrade"
	ActVersion            ActionType = "version"
	ActDoubleInit         ActionType = "double_init"
	ActDoubleServe        ActionType = "double_serve"
	ActRestoreNonEmpty    ActionType = "restore_non_empty"
	ActDataValidateNoArgs ActionType = "data_validate_no_args"
	ActDataInspectNoArgs  ActionType = "data_inspect_no_args"
	ActUpgradeNoArgs      ActionType = "upgrade_no_args"
)

type Step struct {
	Action       ActionType
	Args         []string
	SQLQuery     string
	ExpectFail   bool
	ExpectedExit int
}

type StepRecord struct {
	Step       Step
	Result     blackbox.Result
	ProbeState SystemProbe
	Duration   time.Duration
	Err        error
}

type SystemProbe struct {
	DataDirExists       bool
	InstanceJSONExists  bool
	InstanceID          string
	AdminAccount        string
	InstanceState       string
	DataVersion         string
	CatalogJSONExists   bool
	RunningLockExists   bool
	DatabaseStateExists bool
	IsServing           bool
	DiagnosticsLive     bool
	DiagnosticsReady    bool
	MySQLPortListening  bool
	BackupTarExists     bool
	BackupFileCount     int
	BackupComplete      bool
	TableCount          int
	RowCount            int
}

type StatefulHarness struct {
	t            *testing.T
	runner       blackbox.Runner
	rootDir      string
	dataDir      string
	restoreDir   string
	backupFile   string
	passwordFile string
	password     string
	adminUser    string
	mysqlAddr    string
	diagAddr     string
	serveProcess *blackbox.Process
	serverOutBuf strings.Builder
	history      []StepRecord
}

func NewStatefulHarness(t *testing.T) *StatefulHarness {
	t.Helper()
	root := t.TempDir()
	passFile := filepath.Join(root, "password.txt")
	pass := "secret-password-1234"
	if err := os.WriteFile(passFile, []byte(pass+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &StatefulHarness{
		t:            t,
		runner:       blackbox.Runner{Executable: executable},
		rootDir:      root,
		dataDir:      filepath.Join(root, "instance"),
		restoreDir:   filepath.Join(root, "restore"),
		backupFile:   filepath.Join(root, "backup.tar"),
		passwordFile: passFile,
		password:     pass,
		adminUser:    "admin",
		mysqlAddr:    freeAddress(t),
		diagAddr:     freeAddress(t),
	}
	h.Reset()
	return h
}

func (h *StatefulHarness) Reset() {
	h.t.Helper()
	if h.serveProcess != nil {
		_ = h.serveProcess.Crash()
		_ = h.serveProcess.Wait()
		h.serveProcess = nil
	}
	for range 20 {
		conn, err := net.DialTimeout("tcp", h.mysqlAddr, 50*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
	_ = os.RemoveAll(h.dataDir)
	_ = os.RemoveAll(h.restoreDir)
	_ = os.Remove(h.backupFile)
	h.history = nil

	probe := h.Probe(h.dataDir)
	if probe.DataDirExists || probe.IsServing || probe.MySQLPortListening {
		h.t.Fatalf("Reset failed to restore clean starting state: %+v", probe)
	}
}

func (h *StatefulHarness) Probe(dir string) SystemProbe {
	var p SystemProbe
	if _, err := os.Stat(dir); err == nil {
		p.DataDirExists = true
	}
	instPath := filepath.Join(dir, "instance.json")
	if data, err := os.ReadFile(instPath); err == nil {
		p.InstanceJSONExists = true
		var parsed struct {
			InstanceID   string `json:"instance_id"`
			AdminAccount string `json:"admin_account"`
			State        string `json:"state"`
			DataVersion  string `json:"data_version"`
		}
		if json.Unmarshal(data, &parsed) == nil {
			p.InstanceID = parsed.InstanceID
			p.AdminAccount = parsed.AdminAccount
			p.InstanceState = parsed.State
			p.DataVersion = parsed.DataVersion
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "catalog.json")); err == nil {
		p.CatalogJSONExists = true
	}
	if _, err := os.Stat(filepath.Join(dir, ".running.lock")); err == nil {
		p.RunningLockExists = true
	}
	if _, err := os.Stat(filepath.Join(dir, ".database-state")); err == nil {
		p.DatabaseStateExists = true
	}

	p.IsServing = h.serveProcess != nil
	if h.diagAddr != "" {
		client := &http.Client{Timeout: 300 * time.Millisecond}
		if resp, err := client.Get("http://" + h.diagAddr + "/live"); err == nil {
			resp.Body.Close()
			p.DiagnosticsLive = resp.StatusCode == 200
		}
		if resp, err := client.Get("http://" + h.diagAddr + "/ready"); err == nil {
			resp.Body.Close()
			p.DiagnosticsReady = resp.StatusCode == 200
		}
	}
	if h.mysqlAddr != "" {
		if conn, err := net.DialTimeout("tcp", h.mysqlAddr, 300*time.Millisecond); err == nil {
			conn.Close()
			p.MySQLPortListening = true
		}
	}

	if _, err := os.Stat(h.backupFile); err == nil {
		p.BackupTarExists = true
		if f, err := os.Open(h.backupFile); err == nil {
			defer f.Close()
			tr := tar.NewReader(f)
			count := 0
			for {
				hdr, err := tr.Next()
				if err != nil {
					break
				}
				count++
				if hdr.Name == "backup.json" {
					var manifest struct {
						Complete bool `json:"complete"`
					}
					if json.NewDecoder(tr).Decode(&manifest) == nil {
						p.BackupComplete = manifest.Complete
					}
				}
			}
			p.BackupFileCount = count
		}
	}

	return p
}

func (h *StatefulHarness) Execute(step Step) StepRecord {
	start := time.Now()
	var res blackbox.Result
	var stepErr error

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch step.Action {
	case ActInit:
		args := []string{"init", "--data-directory", h.dataDir, "--initial-password-file", h.passwordFile, "--result=json"}
		if len(step.Args) > 0 {
			args = step.Args
		}
		res = h.runner.Run(ctx, args...)

	case ActDoubleInit:
		res = h.runner.Run(ctx, "init", "--data-directory", h.dataDir, "--initial-password-file", h.passwordFile, "--result=json")

	case ActConfigValidate:
		args := []string{"config", "validate", "--data-directory", h.dataDir, "--result=json"}
		if len(step.Args) > 0 {
			args = step.Args
		}
		res = h.runner.Run(ctx, args...)

	case ActDataValidate:
		args := []string{"data", "validate", "--data-directory", h.dataDir, "--result=json"}
		if len(step.Args) > 0 {
			args = step.Args
		}
		res = h.runner.Run(ctx, args...)

	case ActDataValidateNoArgs:
		res = h.runner.Run(ctx, "data", "validate")

	case ActDataInspect:
		args := []string{"data", "inspect", "--data-directory", h.dataDir, "--result=json"}
		if len(step.Args) > 0 {
			args = step.Args
		}
		res = h.runner.Run(ctx, args...)

	case ActDataInspectNoArgs:
		res = h.runner.Run(ctx, "data", "inspect")

	case ActUpgradeNoArgs:
		res = h.runner.Run(ctx, "upgrade")

	case ActServeStart:
		args := []string{"serve", "--data-directory", h.dataDir, "--mysql-listen-address", h.mysqlAddr, "--diagnostics-listen-address", h.diagAddr, "--format=json"}
		if len(step.Args) > 0 {
			args = step.Args
		}
		proc, err := h.runner.Start(context.Background(), args...)
		if err != nil {
			stepErr = err
			res = blackbox.Result{ExitCode: 1, Err: err}
		} else {
			h.serveProcess = proc
			var event map[string]any
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer waitCancel()
			if err := proc.NextJSONEvent(waitCtx, &event); err != nil {
				stepErr = fmt.Errorf("wait for ready event: %w", err)
				_ = proc.Crash()
				res = proc.Wait()
				h.serveProcess = nil
			} else {
				res = blackbox.Result{ExitCode: 0, Stdout: fmt.Sprintf("%v", event)}
			}
		}

	case ActDoubleServe:
		secondDiag := freeAddress(h.t)
		secondMySQL := freeAddress(h.t)
		res = h.runner.Run(ctx, "serve", "--data-directory", h.dataDir, "--mysql-listen-address", secondMySQL, "--diagnostics-listen-address", secondDiag, "--result=json")

	case ActSQL:
		if h.serveProcess == nil {
			stepErr = errors.New("cannot execute SQL: server is not running")
			res = blackbox.Result{ExitCode: 1}
		} else {
			client := newWireClient(h.t, h.mysqlAddr, h.adminUser, h.password)
			defer client.conn.Close()
			queryRes := client.query(step.SQLQuery)
			if queryRes.err != "" {
				stepErr = errors.New(queryRes.err)
				res = blackbox.Result{ExitCode: 1, Stderr: queryRes.err}
			} else {
				res = blackbox.Result{ExitCode: 0, Stdout: fmt.Sprintf("rows=%d", len(queryRes.rows))}
			}
		}

	case ActBackupCreate:
		args := []string{"backup", "create", "--address", h.mysqlAddr, "--account", h.adminUser, "--password-file", h.passwordFile, "--output", h.backupFile, "--result=json"}
		if len(step.Args) > 0 {
			args = step.Args
		}
		res = h.runner.Run(ctx, args...)

	case ActBackupInspect:
		target := h.backupFile
		if len(step.Args) > 0 {
			target = step.Args[0]
		}
		res = h.runner.Run(ctx, "backup", "inspect", "--backup", target, "--result=json")

	case ActShutdownCLI:
		res = h.runner.Run(ctx, "shutdown", "--address", h.mysqlAddr, "--account", h.adminUser, "--password-file", h.passwordFile, "--yes", "--result=json")
		if h.serveProcess != nil {
			waitRes := h.serveProcess.Wait()
			if waitRes.ExitCode != 0 {
				stepErr = fmt.Errorf("serve process exited with %d: %s", waitRes.ExitCode, waitRes.Stderr)
			}
			h.serveProcess = nil
		}

	case ActShutdownSIGTERM:
		if h.serveProcess != nil {
			if err := h.serveProcess.Stop(); err != nil {
				stepErr = err
			}
			res = h.serveProcess.Wait()
			h.serveProcess = nil
		}

	case ActKillServer:
		if h.serveProcess != nil {
			_ = h.serveProcess.Crash()
			res = h.serveProcess.Wait()
			h.serveProcess = nil
		}

	case ActRestore:
		target := h.restoreDir
		if len(step.Args) > 0 {
			target = step.Args[0]
		}
		res = h.runner.Run(ctx, "restore", "--backup", h.backupFile, "--data-directory", target, "--result=json")

	case ActRestoreNonEmpty:
		_ = os.MkdirAll(h.restoreDir, 0o700)
		_ = os.WriteFile(filepath.Join(h.restoreDir, "conflict.txt"), []byte("data"), 0o600)
		res = h.runner.Run(ctx, "restore", "--backup", h.backupFile, "--data-directory", h.restoreDir, "--result=json")

	case ActUpgrade:
		args := []string{"upgrade", "--data-directory", h.dataDir, "--backup", h.backupFile, "--target-version", "0.1.1", "--yes", "--result=json"}
		if len(step.Args) > 0 {
			args = step.Args
		}
		res = h.runner.Run(ctx, args...)

	case ActVersion:
		res = h.runner.Run(ctx, "version", "--result=json")
	}

	probe := h.Probe(h.dataDir)
	rec := StepRecord{
		Step:       step,
		Result:     res,
		ProbeState: probe,
		Duration:   time.Since(start),
		Err:        stepErr,
	}
	h.history = append(h.history, rec)
	return rec
}

func (h *StatefulHarness) ValidateInvariants(rec StepRecord) []string {
	var violations []string

	hasJSONResult := false
	for _, arg := range rec.Step.Args {
		if strings.HasPrefix(arg, "--result=json") {
			hasJSONResult = true
		}
	}
	if rec.Step.Action == ActInit || rec.Step.Action == ActConfigValidate || rec.Step.Action == ActDataValidate ||
		rec.Step.Action == ActDataInspect || rec.Step.Action == ActBackupCreate || rec.Step.Action == ActBackupInspect ||
		rec.Step.Action == ActShutdownCLI || rec.Step.Action == ActRestore || rec.Step.Action == ActUpgrade ||
		rec.Step.Action == ActVersion || rec.Step.Action == ActDoubleInit || rec.Step.Action == ActDoubleServe ||
		rec.Step.Action == ActRestoreNonEmpty {
		hasJSONResult = true
	}

	if hasJSONResult && rec.Result.Stdout != "" {
		var env struct {
			Schema    string         `json:"schema"`
			Status    string         `json:"status"`
			ExitClass string         `json:"exit_class"`
			ExitCode  *int           `json:"exit_code"`
			Details   map[string]any `json:"details"`
		}
		lines := strings.Split(strings.TrimSpace(rec.Result.Stdout), "\n")
		lastLine := lines[len(lines)-1]
		if err := json.Unmarshal([]byte(lastLine), &env); err == nil && env.Schema == "database.operator.result/v1" {
			if env.ExitCode != nil && *env.ExitCode != rec.Result.ExitCode {
				violations = append(violations, fmt.Sprintf("exit code mismatch: envelope reported exit_code=%d but process exited with %d", *env.ExitCode, rec.Result.ExitCode))
			}
			if rec.Result.ExitCode == 0 {
				switch rec.Step.Action {
				case ActBackupInspect:
					for _, key := range []string{"completeness", "integrity", "source_instance_id", "data_version", "created_at", "compatibility"} {
						if env.Details[key] == nil || env.Details[key] == "" {
							violations = append(violations, fmt.Sprintf("backup inspect details missing key %q: %v", key, env.Details))
						}
					}
				case ActRestore:
					for _, key := range []string{"artifact_path", "target_directory", "source_instance_id", "data_version", "state"} {
						if env.Details[key] == nil || env.Details[key] == "" {
							violations = append(violations, fmt.Sprintf("restore details missing key %q: %v", key, env.Details))
						}
					}
				case ActUpgrade:
					for _, key := range []string{"instance_id", "data_directory", "previous_version", "resulting_version", "state"} {
						if env.Details[key] == nil || env.Details[key] == "" {
							violations = append(violations, fmt.Sprintf("upgrade details missing key %q: %v", key, env.Details))
						}
					}
				}
			}
		}
	}

	if rec.Step.Action == ActDataInspect && rec.ProbeState.IsServing {
		var env struct {
			Details map[string]any `json:"details"`
		}
		lines := strings.Split(strings.TrimSpace(rec.Result.Stdout), "\n")
		lastLine := lines[len(lines)-1]
		if err := json.Unmarshal([]byte(lastLine), &env); err == nil {
			if state, ok := env.Details["state"].(string); !ok || state != "serving" {
				violations = append(violations, fmt.Sprintf("data inspect during active serve reported state=%q, expected \"serving\"", state))
			}
		}
	}

	if rec.Step.Action == ActDataValidateNoArgs || rec.Step.Action == ActDataInspectNoArgs || rec.Step.Action == ActUpgradeNoArgs {
		if strings.Contains(rec.Result.Stdout, "is not implemented") {
			violations = append(violations, fmt.Sprintf("spurious not implemented failure on %s: %s", rec.Step.Action, rec.Result.Stdout))
		}
		if rec.Result.ExitCode != 2 {
			violations = append(violations, fmt.Sprintf("expected exit_code 2 (invalid_input) on %s without required flags, got %d", rec.Step.Action, rec.Result.ExitCode))
		}
	}

	if rec.Step.Action == ActRestoreNonEmpty {
		if rec.Result.ExitCode != 3 {
			violations = append(violations, fmt.Sprintf("restore to non-empty directory returned exit_code=%d, expected 3 (precondition)", rec.Result.ExitCode))
		}
	}

	if rec.Step.Action == ActDoubleServe {
		if rec.Result.ExitCode != 3 {
			violations = append(violations, fmt.Sprintf("second serve returned exit_code=%d, expected 3 (precondition)", rec.Result.ExitCode))
		}
	}

	return violations
}

func TestStatefulSequenceCampaign(t *testing.T) {
	h := NewStatefulHarness(t)

	sequences := []struct {
		Name  string
		Steps []Step
	}{
		{
			Name: "FullLifecycle_Init_Serve_SQL_Backup_Restore",
			Steps: []Step{
				{Action: ActInit},
				{Action: ActConfigValidate},
				{Action: ActDataValidate},
				{Action: ActDataInspect},
				{Action: ActServeStart},
				{Action: ActSQL, SQLQuery: "CREATE DATABASE testdb"},
				{Action: ActSQL, SQLQuery: "CREATE TABLE testdb.users (id INT PRIMARY KEY, name VARCHAR(50))"},
				{Action: ActSQL, SQLQuery: "INSERT INTO testdb.users VALUES (1, 'alice'), (2, 'bob')"},
				{Action: ActBackupCreate},
				{Action: ActBackupInspect},
				{Action: ActShutdownCLI},
				{Action: ActDataValidate},
				{Action: ActDataInspect},
				{Action: ActRestore},
			},
		},
		{
			Name: "Exclusivity_DoubleServe_And_DoubleInit",
			Steps: []Step{
				{Action: ActInit},
				{Action: ActDoubleInit, ExpectFail: true, ExpectedExit: 3},
				{Action: ActServeStart},
				{Action: ActDataInspect},
				{Action: ActDoubleServe, ExpectFail: true},
				{Action: ActShutdownCLI},
			},
		},
		{
			Name: "Crash_Recovery_State_Preservation",
			Steps: []Step{
				{Action: ActInit},
				{Action: ActServeStart},
				{Action: ActSQL, SQLQuery: "CREATE DATABASE crashdb"},
				{Action: ActSQL, SQLQuery: "CREATE TABLE crashdb.events (id INT PRIMARY KEY, event VARCHAR(50))"},
				{Action: ActSQL, SQLQuery: "INSERT INTO crashdb.events VALUES (10, 'boot')"},
				{Action: ActKillServer},
				{Action: ActServeStart},
				{Action: ActSQL, SQLQuery: "INSERT INTO crashdb.events VALUES (20, 'recovered')"},
				{Action: ActShutdownSIGTERM},
			},
		},
		{
			Name: "Restore_Target_Precondition_Checking",
			Steps: []Step{
				{Action: ActInit},
				{Action: ActServeStart},
				{Action: ActBackupCreate},
				{Action: ActShutdownCLI},
				{Action: ActRestoreNonEmpty, ExpectFail: true, ExpectedExit: 3},
			},
		},
		{
			Name: "Upgrade_Workflow_Details",
			Steps: []Step{
				{Action: ActInit},
				{Action: ActServeStart},
				{Action: ActBackupCreate},
				{Action: ActShutdownCLI},
				{Action: ActUpgrade},
			},
		},
		{
			Name: "CLI_Bare_Command_Input_Validation",
			Steps: []Step{
				{Action: ActDataValidateNoArgs, ExpectFail: true, ExpectedExit: 2},
				{Action: ActDataInspectNoArgs, ExpectFail: true, ExpectedExit: 2},
				{Action: ActUpgradeNoArgs, ExpectFail: true, ExpectedExit: 2},
			},
		},
	}

	for _, seq := range sequences {
		t.Run(seq.Name, func(t *testing.T) {
			h.Reset()
			var seqViolations []string
			for idx, step := range seq.Steps {
				rec := h.Execute(step)
				violations := h.ValidateInvariants(rec)
				if len(violations) > 0 {
					t.Logf("Step %d (%s) violations: %v", idx, step.Action, violations)
					seqViolations = append(seqViolations, violations...)
				}
			}

			if len(seqViolations) > 0 {
				// Step 7: Verify stability by replaying 3 times
				t.Logf("Verifying stability of failures across 3 replays...")
				stableCount := 0
				for replay := 1; replay <= 3; replay++ {
					h.Reset()
					failedAgain := false
					for _, step := range seq.Steps {
						rec := h.Execute(step)
						if len(h.ValidateInvariants(rec)) > 0 {
							failedAgain = true
							break
						}
					}
					if failedAgain {
						stableCount++
					}
				}
				t.Errorf("Sequence %s failed invariants: %v", seq.Name, seqViolations)
			}
		})
	}
}
