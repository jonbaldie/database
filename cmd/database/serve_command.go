package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jonbaldie/database/internal/lifecycle"
)

const serveUsage = "Usage: database serve [--format=human|json] [--config PATH] [--data-directory PATH] [--mysql-listen-address HOST:PORT] [--tls-certificate-file PATH --tls-private-key-file PATH] [--diagnostics-listen-address HOST:PORT] [--log-format=json|text]"

func serve(args []string, stdout, stderr io.Writer) int {
	if isCommandHelp(args) {
		fmt.Fprintln(stdout, serveUsage)
		return 0
	}
	return runServe(args, stdout, stderr)
}

func isCommandHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h")
}

func runServe(args []string, stdout, stderr io.Writer) int {
	return runServeWithReporter(args, stdout, stderr)
}

func runServeWithReporter(args []string, stdout, stderr io.Writer) int {
	reporter, configurationArgs, err := newServeReporter(args, stdout, stderr)
	if err != nil {
		return reportServeOutputFailure(reporter, err, stderr)
	}
	opts, err := parseServeFlags(configurationArgs)
	if err != nil {
		return reportServeConfigurationFailure(reporter, err, stderr)
	}
	return serveLifecycleWithReporter(opts, reporter)
}

func newServeReporter(args []string, stdout, stderr io.Writer) (*operationReporter, []string, error) {
	output, filtered, err := parseCommandOutput(args)
	if !output.resultSet && !output.formatSet && !output.progressSet {
		output.legacy = true
	}
	if output.legacy && !output.progressSet {
		output.progress = "none"
	}
	return newOperationReporter("serve", output, stdout, stderr), filtered, err
}

func usesLegacyServeOutput(output commandOutput) bool {
	return output.legacy && !output.resultSet && !output.progressSet
}

func usesLegacyHumanServeOutput(output commandOutput) bool {
	return usesLegacyServeOutput(output) && output.result == "human"
}

func reportServeOutputFailure(reporter *operationReporter, err error, stderr io.Writer) int {
	if usesLegacyHumanServeOutput(reporter.output) {
		return serveInputFailure(stderr, err)
	}
	if reporter.output.resultSet || reporter.output.formatSet || reporter.output.progressSet {
		reporter.output.result = "json"
	}
	return reporter.failure("invalid_input", "", err.Error(), nil)
}

func reportServeConfigurationFailure(reporter *operationReporter, err error, stderr io.Writer) int {
	if usesLegacyHumanServeOutput(reporter.output) {
		return serveConfigurationFailure(stderr, err)
	}
	return reporter.failure(configurationClass(err), "", err.Error(), nil)
}

func serveLifecycleWithReporter(opts lifecycle.Options, reporter *operationReporter) int {
	ctx := context.Background()
	opts.OperationID = reporter.id
	state := ""
	recovered := false
	details := map[string]any{}
	reporter.progress("starting")
	err := lifecycle.Serve(ctx, opts, func(event lifecycle.Event) {
		state = event.State
		recovered = recovered || event.Recovered
		recordServeEvent(reporter, event, details)
	})
	if err != nil {
		return reportServeLifecycleFailure(reporter, err, details)
	}
	if state == "stopped" || state == "" {
		details["state"] = "stopped"
	}
	details["data_directory"] = opts.DataDirectory
	details["recovered"] = recovered
	return reportServeLifecycleSuccess(reporter, details)
}

func recordServeEvent(reporter *operationReporter, event lifecycle.Event, details map[string]any) {
	if usesLegacyServeOutput(reporter.output) {
		event.OperationID = reporter.id
		if reporter.output.result == "json" {
			_ = json.NewEncoder(reporter.stdout).Encode(event)
		} else {
			writeHumanServeEvent(reporter.stdout, event)
		}
	} else if isServeProgressPhase(event.State) {
		reporter.progress(event.State)
	}
	if event.State == "ready" {
		details["state"] = "ready"
		details["instance_id"] = event.InstanceID
		details["ready_at"] = event.RecordedAt
		if event.DiagnosticsAddress != "" {
			details["diagnostics_address"] = event.DiagnosticsAddress
		}
		if len(event.Warnings) != 0 {
			details["warnings"] = event.Warnings
		}
	}
	if event.State == "stopping" {
		details["stopping_at"] = event.RecordedAt
		details["shutdown_reason"] = event.ShutdownReason
	}
}

func isServeProgressPhase(phase string) bool {
	switch phase {
	case "recovering", "ready", "stopping":
		return true
	default:
		return false
	}
}

func reportServeLifecycleFailure(reporter *operationReporter, err error, details map[string]any) int {
	if usesLegacyHumanServeOutput(reporter.output) {
		class := serveExitClass(err)
		code := operatorExitCode(class)
		if class == "operation_failed" {
			code = 1
		}
		fmt.Fprintf(reporter.stderr, "database serve: %v\n", err)
		return code
	}
	return reporter.failure(serveExitClass(err), "", err.Error(), details)
}

func reportServeLifecycleSuccess(reporter *operationReporter, details map[string]any) int {
	if usesLegacyHumanServeOutput(reporter.output) {
		fmt.Fprintf(reporter.stdout, "database serve: success (operation_id=%s)\n", reporter.id)
		return 0
	}
	return reporter.success(details)
}

func serveExitClass(err error) string {
	if errors.Is(err, lifecycle.ErrPrecondition) {
		return "precondition"
	}
	return "operation_failed"
}

func serveInputFailure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "database serve: invalid_input: %v\n", err)
	return 2
}

func serveConfigurationFailure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "database serve: %s: %v\n", configurationClass(err), err)
	return 2
}

func writeHumanServeEvent(stdout io.Writer, event lifecycle.Event) {
	for _, warning := range event.Warnings {
		fmt.Fprintf(stdout, "database: WARNING [%s] %s\n", warning.Code, warning.Summary)
	}
	if event.State != "ready" {
		fmt.Fprintf(stdout, "database: %s\n", event.State)
		return
	}
	if event.DiagnosticsAddress == "" {
		fmt.Fprintln(stdout, "database: ready")
		return
	}
	fmt.Fprintf(stdout, "database: ready (diagnostics=%s)\n", event.DiagnosticsAddress)
}
