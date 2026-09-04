package main

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// commandOutput describes the two public operator streams. The terminal
// result is written to stdout. Progress and human diagnostics use stderr.
type commandOutput struct {
	result   string
	progress string
	legacy   bool
}

func defaultCommandOutput() commandOutput { return commandOutput{result: "human", progress: "auto"} }

func containsOutputControl(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || strings.HasPrefix(arg, "--result") || strings.HasPrefix(arg, "--progress") || strings.HasPrefix(arg, "--format") {
			return true
		}
	}
	return false
}

// parseCommandOutput removes output controls from args. --format is retained
// as a compatibility alias for clients of the first command-line contract.
func parseCommandOutput(args []string, acceptFormatAlias bool) (commandOutput, []string, error) {
	parser := commandOutputParser{output: defaultCommandOutput(), filtered: make([]string, 0, len(args)), acceptFormatAlias: acceptFormatAlias}
	for index, argumentCount := 0, len(args); index < argumentCount; index++ {
		next, err := parser.consume(args, index)
		if err != nil {
			return parser.output, nil, err
		}
		index = next
	}
	return parser.output, parser.filtered, nil
}

type commandOutputParser struct {
	output            commandOutput
	filtered          []string
	acceptFormatAlias bool
	resultSeen        bool
	progressSeen      bool
}

func (parser *commandOutputParser) consume(args []string, index int) (int, error) {
	name, _, _ := strings.Cut(args[index], "=")
	if parser.isResult(name) {
		return parser.consumeResult(args, index)
	}
	if name == "--progress" {
		return parser.consumeProgress(args, index)
	}
	parser.filtered = append(parser.filtered, args[index])
	return index, nil
}

func (parser *commandOutputParser) isResult(name string) bool {
	return name == "--result" || name == "--json" || parser.acceptFormatAlias && name == "--format"
}

func (parser *commandOutputParser) consumeResult(args []string, index int) (int, error) {
	if parser.resultSeen {
		return index, fmt.Errorf("repeated result format")
	}
	parser.resultSeen = true
	name, value, hasValue := strings.Cut(args[index], "=")
	if name == "--json" {
		if hasValue {
			return index, fmt.Errorf("--json does not take a value")
		}
		value = "json"
	} else if !hasValue {
		if index+1 >= len(args) {
			return index, fmt.Errorf("%s requires a value", name)
		}
		index++
		value = args[index]
	}
	if value == "text" {
		value = "human"
	}
	if value != "human" && value != "json" {
		return index, fmt.Errorf("result must be human or json")
	}
	parser.output.result = value
	return index, nil
}

func (parser *commandOutputParser) consumeProgress(args []string, index int) (int, error) {
	if parser.progressSeen {
		return index, fmt.Errorf("repeated progress mode")
	}
	parser.progressSeen = true
	_, value, hasValue := strings.Cut(args[index], "=")
	if !hasValue {
		if index+1 >= len(args) {
			return index, fmt.Errorf("--progress requires a value")
		}
		index++
		value = args[index]
	}
	if value != "auto" && value != "human" && value != "json" && value != "none" {
		return index, fmt.Errorf("progress must be auto, human, json, or none")
	}
	parser.output.progress = value
	return index, nil
}

type operationReporter struct {
	command  string
	id       string
	started  time.Time
	output   commandOutput
	stdout   io.Writer
	stderr   io.Writer
	sequence int
}

func newOperationReporter(command string, output commandOutput, stdout, stderr io.Writer) *operationReporter {
	return &operationReporter{command: command, id: newOperationID(), started: time.Now().UTC(), output: output, stdout: stdout, stderr: stderr}
}

func (reporter *operationReporter) progress(phase string) {
	mode := reporter.output.progress
	if mode == "auto" {
		if !isTerminal(reporter.stderr) {
			return
		}
		mode = "human"
	}
	if mode == "none" {
		return
	}
	reporter.sequence++
	if mode == "human" {
		fmt.Fprintf(reporter.stderr, "%s: %s (operation_id=%s)\n", reporter.command, phase, reporter.id)
		return
	}
	_ = json.NewEncoder(reporter.stderr).Encode(map[string]any{
		"schema": "database.operator.progress/v1", "record_type": "progress",
		"operation_id": reporter.id, "command": reporter.command,
		"sequence": reporter.sequence, "recorded_at": time.Now().UTC().Format(time.RFC3339Nano), "phase": phase,
	})
}

func (reporter *operationReporter) success(details map[string]any) int {
	return reporter.terminal("success", "success", "", "", details)
}

func (reporter *operationReporter) failure(class, code, summary string, details map[string]any) int {
	return reporter.terminal("failure", class, code, summary, details)
}

func (reporter *operationReporter) terminal(status, exitClass, diagnosticCode, summary string, details map[string]any) int {
	if details == nil {
		details = map[string]any{}
	}
	exitCode := operatorExitCode(exitClass)
	if reporter.output.legacy && exitClass == "operation_failed" {
		exitCode = 1
	}
	finished := time.Now().UTC()
	if reporter.output.result == "json" {
		return reporter.writeJSONTerminal(status, exitClass, diagnosticCode, summary, details, finished, exitCode)
	}
	return reporter.writeHumanTerminal(status, exitClass, diagnosticCode, summary, exitCode)
}

func (reporter *operationReporter) writeJSONTerminal(status, exitClass, diagnosticCode, summary string, details map[string]any, finished time.Time, exitCode int) int {
	diagnostics := diagnosticRecords(exitClass, diagnosticCode, summary)
	result := map[string]any{
		"schema": "database.operator.result/v1", "record_type": "result",
		"operation_id": reporter.id, "command": reporter.command, "operation": reporter.command,
		"status": status, "success": status == "success", "exit_class": exitClass, "exit_code": exitCode,
		"started_at": reporter.started.Format(time.RFC3339Nano), "finished_at": finished.Format(time.RFC3339Nano),
		"duration_ms": finished.Sub(reporter.started).Milliseconds(), "details": details, "diagnostics": diagnostics,
	}
	for key, value := range details {
		result[key] = value
	}
	if summary != "" {
		result["diagnostic"] = summary
	}
	if err := json.NewEncoder(reporter.stdout).Encode(result); err != nil {
		return 6
	}
	return exitCode
}

func diagnosticRecords(exitClass, diagnosticCode, summary string) []map[string]any {
	if summary == "" {
		return []map[string]any{}
	}
	if diagnosticCode == "" {
		diagnosticCode = diagnosticCodeForClass(exitClass)
	}
	return []map[string]any{{"code": diagnosticCode, "severity": "error", "summary": summary}}
}

func (reporter *operationReporter) writeHumanTerminal(status, exitClass, diagnosticCode, summary string, exitCode int) int {
	if status == "success" {
		fmt.Fprintf(reporter.stdout, "%s: success (operation_id=%s)\n", reporter.command, reporter.id)
		return 0
	}
	fmt.Fprintf(reporter.stdout, "%s: %s (operation_id=%s)\n", reporter.command, exitClass, reporter.id)
	if summary != "" {
		if diagnosticCode == "" {
			diagnosticCode = diagnosticCodeForClass(exitClass)
		}
		fmt.Fprintf(reporter.stderr, "%s [%s]: %s\n", reporter.command, diagnosticCode, summary)
	}
	return exitCode
}

func diagnosticCodeForClass(class string) string {
	if class == "invalid_artifact" {
		return "artifact_unusable"
	}
	return class
}

func newOperationID() string {
	bytes := make([]byte, 16)
	if _, err := cryptorand.Read(bytes); err == nil {
		return "op-" + hex.EncodeToString(bytes)
	}
	return fmt.Sprintf("op-%d", time.Now().UnixNano())
}

func isTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func operatorExitCode(class string) int {
	switch class {
	case "success":
		return 0
	case "invalid_input":
		return 2
	case "precondition":
		return 3
	case "access":
		return 4
	case "invalid_artifact":
		return 5
	case "operation_failed":
		return 6
	case "interrupted":
		return 7
	default:
		return 6
	}
}

// These helpers preserve the old internal API while producing the complete
// terminal envelope when a workflow has not yet migrated to a reporter.
func writeOperatorResult(stdout io.Writer, operation, operationID string, success bool, exitClass, diagnostic string) int {
	reporter := &operationReporter{command: operation, id: operationID, started: time.Now().UTC(), output: commandOutput{result: "json", progress: "none"}, stdout: stdout}
	if success {
		return reporter.success(nil)
	}
	return reporter.failure(exitClass, diagnosticCodeForClass(exitClass), diagnostic, nil)
}

func writeOperatorFailure(stdout io.Writer, operation, operationID, class string, code int, message string) int {
	reporter := &operationReporter{command: operation, id: operationID, started: time.Now().UTC(), output: commandOutput{result: "json", progress: "none", legacy: true}, stdout: stdout}
	resultCode := reporter.failure(class, diagnosticCodeForClass(class), message, nil)
	if code != 0 && resultCode != code {
		return code
	}
	return resultCode
}

func operatorResult(operation, operationID string, success bool, exitClass, diagnostic string) map[string]any {
	result := map[string]any{
		"schema": "database.operator.result/v1", "record_type": "result", "operation": operation, "command": operation, "operation_id": operationID,
		"status": map[bool]string{true: "success", false: "failure"}[success], "success": success, "exit_class": exitClass, "exit_code": operatorExitCode(exitClass),
	}
	if diagnostic != "" {
		result["diagnostic"] = diagnostic
		result["diagnostics"] = []map[string]any{{"code": diagnosticCodeForClass(exitClass), "severity": "error", "summary": diagnostic}}
	} else {
		result["diagnostics"] = []map[string]any{}
	}
	return result
}
