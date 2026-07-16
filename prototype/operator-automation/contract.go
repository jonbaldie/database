package main

// This file is the portable, pure product-contract model behind the throwaway
// terminal browser. It describes observable inputs and outputs only.

type commandContract struct {
	name           string
	inputs         []string
	secret         string
	progressPhases []string
	successFields  []string
	failure        failureCase
}

type failureCase struct {
	exitClass  string
	code       int
	diagnostic string
	meaning    string
}

var commands = []commandContract{
	{
		name: "database init",
		inputs: []string{
			"--data-directory PATH (required; new or empty)",
			"--initial-account NAME (required)",
			"exactly one: --initial-password-file PATH | --initial-password-stdin",
		},
		secret:         "Initial password: file or stdin only; never argv or environment",
		progressPhases: []string{"preflight", "initializing", "validating"},
		successFields:  []string{"instance_id", "data_directory", "initial_account", "state=stopped"},
		failure:        failureCase{"precondition", 3, "target_not_empty", "Target cannot be initialized; no initialized instance exists"},
	},
	{
		name: "database serve",
		inputs: []string{
			"optional --config PATH plus the closed server-setting flags",
			"data_directory comes from the effective server configuration",
		},
		secret:         "No command secret; TLS private-key path is redacted in all records",
		progressPhases: []string{"starting", "recovering", "ready", "stopping"},
		successFields:  []string{"instance_id", "ready_at", "stopped_at", "shutdown_reason", "state=stopped"},
		failure:        failureCase{"invalid_artifact", 5, "durable_state_corrupt", "Normal service was refused and final state is failed"},
	},
	{
		name: "database shutdown",
		inputs: append(onlineInputs(),
			"--yes for non-interactive acknowledgement"),
		secret:         onlineSecret,
		progressPhases: []string{"connecting", "requesting", "draining", "stopped"},
		successFields:  []string{"instance_id", "requested_at", "stopped_at", "state=stopped"},
		failure:        failureCase{"access", 4, "permission_denied", "Shutdown was not accepted; server state is unchanged"},
	},
	{
		name: "database backup create",
		inputs: append(onlineInputs(),
			"--output PATH (required; must not exist)"),
		secret:         onlineSecret,
		progressPhases: []string{"connecting", "capturing", "writing", "validating"},
		successFields:  []string{"backup_path", "source_instance_id", "created_at", "source_version", "backup_version", "size_bytes", "complete=true"},
		failure:        failureCase{"operation_failed", 6, "destination_write_failed", "No complete backup exists; cleanup_required says whether partial output remains"},
	},
	{
		name:           "database backup inspect",
		inputs:         []string{"--backup PATH (required)"},
		secret:         "No credential; artifact content and account data are never emitted",
		progressPhases: []string{"reading", "validating"},
		successFields:  []string{"backup_path", "complete", "integrity", "created_at", "source_instance_id", "source_version", "backup_version", "compatibility"},
		failure:        failureCase{"invalid_artifact", 5, "backup_incomplete", "Artifact is explicitly not a usable backup"},
	},
	{
		name: "database restore",
		inputs: []string{
			"--backup PATH (required; complete and compatible)",
			"--data-directory PATH (required; new or empty)",
		},
		secret:         "No credential; backup content and restored account data are never emitted",
		progressPhases: []string{"preflight", "restoring", "validating"},
		successFields:  []string{"backup_path", "data_directory", "instance_id", "source_instance_id", "data_version", "state=stopped"},
		failure:        failureCase{"invalid_artifact", 5, "backup_corrupt", "No restored instance exists; cleanup_required reports residual output"},
	},
	{
		name: "database upgrade",
		inputs: []string{
			"--data-directory PATH (required; offline)",
			"--backup PATH (required; exact pre-upgrade state)",
			"--yes for non-interactive acknowledgement",
		},
		secret:         "No credential; backup and durable contents are never emitted",
		progressPhases: []string{"preflight", "upgrading", "validating"},
		successFields:  []string{"instance_id", "data_directory", "from_data_version", "to_data_version", "state=stopped"},
		failure:        failureCase{"interrupted", 7, "upgrade_interrupted", "Directory is upgrade-incomplete; rerun the same target upgrade"},
	},
	{
		name: "database config validate",
		inputs: []string{
			"optional --config PATH plus the closed server-setting flags",
			"same precedence and validation as database serve",
		},
		secret:         "TLS private-key path and any later secret setting are redacted",
		progressPhases: []string{"loading", "validating"},
		successFields:  []string{"valid=true", "effective_settings{name,value,source,redacted}"},
		failure:        failureCase{"invalid_input", 2, "unknown_setting", "No effective configuration is accepted"},
	},
	{
		name:           "database data validate",
		inputs:         []string{"--data-directory PATH (required; offline)"},
		secret:         "No credential; application values and internal file layout are never emitted",
		progressPhases: []string{"preflight", "validating"},
		successFields:  []string{"instance_id", "data_directory", "integrity=clean", "checked_at", "findings=[]"},
		failure:        failureCase{"invalid_artifact", 5, "durable_state_corrupt", "Findings identify affected public namespaces or objects without exposing values"},
	},
	{
		name:           "database data inspect",
		inputs:         []string{"--data-directory PATH (required; offline)"},
		secret:         "No credential; application values and internal file layout are never emitted",
		progressPhases: []string{"reading"},
		successFields:  []string{"instance_id", "data_directory", "data_version", "created_by_version", "compatible_versions", "recovery_required", "upgrade_required", "state"},
		failure:        failureCase{"precondition", 3, "data_directory_active", "Inspection refuses a directory owned by a running server"},
	},
	{
		name:           "database version",
		inputs:         []string{"no command-specific inputs"},
		secret:         "No credential or secret input",
		progressPhases: nil,
		successFields:  []string{"product_version", "build_identity", "platform", "data_compatibility", "backup_compatibility", "mysql_application_compatibility_profile"},
		failure:        failureCase{"operation_failed", 6, "invalid_build_identity", "Executable cannot report a valid supported build identity"},
	},
}

const onlineSecret = "Database-account password: exactly one of --password-file PATH or --password-stdin"

func onlineInputs() []string {
	return []string{
		"--address HOST:PORT (default 127.0.0.1:3306)",
		"--account NAME (required)",
		"exactly one: --password-file PATH | --password-stdin",
		"--tls disabled|verify-full (default disabled)",
		"optional --tls-ca-file PATH and --tls-server-name NAME with verify-full",
	}
}

func resultRecord(c commandContract, failure bool) map[string]any {
	status := "success"
	exitClass := "success"
	exitCode := 0
	diagnostics := []map[string]any{}
	details := map[string]any{"fields": c.successFields}
	if failure {
		status = "failure"
		exitClass = c.failure.exitClass
		exitCode = c.failure.code
		diagnostics = []map[string]any{{
			"code":     c.failure.diagnostic,
			"severity": "error",
			"summary":  c.failure.meaning,
		}}
		details = map[string]any{"terminal_state": c.failure.meaning}
	}
	return map[string]any{
		"schema":       "database.operator.result/v1",
		"record_type":  "result",
		"operation_id": "01K0DEMO7Y8M9N0P1Q2R3S4T5V",
		"command":      c.name,
		"status":       status,
		"exit_class":   exitClass,
		"exit_code":    exitCode,
		"started_at":   "2026-07-16T14:30:00Z",
		"finished_at":  "2026-07-16T14:30:04Z",
		"duration_ms":  4000,
		"details":      details,
		"diagnostics":  diagnostics,
	}
}

func progressRecord(c commandContract, phaseIndex int) map[string]any {
	phase := c.progressPhases[phaseIndex]
	record := map[string]any{
		"schema":       "database.operator.progress/v1",
		"record_type":  "progress",
		"operation_id": "01K0DEMO7Y8M9N0P1Q2R3S4T5V",
		"command":      c.name,
		"sequence":     phaseIndex + 1,
		"recorded_at":  "2026-07-16T14:30:02Z",
		"phase":        phase,
	}
	if phaseIndex > 0 {
		record["work"] = map[string]any{"completed": 24, "total": 100, "unit": "percent"}
	}
	return record
}
