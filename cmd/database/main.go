package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/buildinfo"
)

var exitProcess = os.Exit

func main() { exitProcess(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	handler, ok := commandHandlerFor(args[0])
	if !ok {
		fmt.Fprintf(stderr, "database: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	return handler(commandInvocation{args: args, stdout: stdout, stderr: stderr})
}

func operatorCommand(args []string, stdout io.Writer) int {
	operation := operatorName(args)
	operationID := newOperationID()
	if len(args) == 0 {
		return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, "operator command requires an operation")
	}
	if args[0] == "backup" || args[0] == "restore" {
		return backupRestoreCommand(args, stdout)
	}
	if args[0] == "upgrade" {
		if len(args) == 1 {
			return unsupportedSimpleOperatorCommand(args, stdout, operation, operationID)
		}
		return upgradeCommand(args[1:], stdout)
	}
	return unsupportedOperatorCommand(args, stdout, operation, operationID)
}

func unsupportedOperatorCommand(args []string, stdout io.Writer, operation, operationID string) int {
	if args[0] == "data" {
		return unsupportedDataCommand(args, stdout, operation, operationID)
	}
	if args[0] == "upgrade" || args[0] == "shutdown" {
		return unsupportedSimpleOperatorCommand(args, stdout, operation, operationID)
	}
	return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, fmt.Sprintf("unsupported operator command %q", args[0]))
}

func unsupportedDataCommand(args []string, stdout io.Writer, operation, operationID string) int {
	if len(args) < 2 {
		return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, "data requires validate or inspect")
	}
	operation = strings.Join(args[:2], " ")
	if args[1] != "validate" && args[1] != "inspect" {
		return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, fmt.Sprintf("unsupported data operation %q", args[1]))
	}
	if hasUnknownOperatorFlag(args[2:]) {
		return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, "unknown operator flag")
	}
	return writeOperatorFailure(stdout, operation, operationID, "operation_failed", 1, fmt.Sprintf("%s is not implemented", operation))
}

func unsupportedSimpleOperatorCommand(args []string, stdout io.Writer, operation, operationID string) int {
	if hasUnknownOperatorFlag(args[1:]) {
		return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, "unknown operator flag")
	}
	return writeOperatorFailure(stdout, operation, operationID, "operation_failed", 1, fmt.Sprintf("%s is not implemented", args[0]))
}

func operatorName(args []string) string {
	if len(args) == 0 {
		return "operator"
	}
	if len(args) > 1 && args[0] == "backup" {
		return strings.Join(args[:2], " ")
	}
	if len(args) > 1 && args[0] == "data" && !strings.HasPrefix(args[1], "-") {
		return strings.Join(args[:2], " ")
	}
	return args[0]
}

func operatorOptions(args []string, allowed ...string) (map[string]string, error) {
	known := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		known[name] = true
	}
	values := make(map[string]string, len(allowed))
	for index, argumentCount := 0, len(args); index < argumentCount; index++ {
		arg := args[index]
		name, value, hasValue := strings.Cut(arg, "=")
		if !hasValue {
			name = arg
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return nil, fmt.Errorf("%s requires a value", name)
			}
			index++
			value = args[index]
		}
		if !known[name] {
			return nil, fmt.Errorf("unknown flag %q", name)
		}
		if value == "" {
			return nil, fmt.Errorf("%s has an empty value", name)
		}
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("repeated flag %s", name)
		}
		values[name] = value
	}
	return values, nil
}

func hasUnknownOperatorFlag(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return true
		}
	}
	return false
}

func newOperationID() string { return fmt.Sprintf("op-%d", time.Now().UnixNano()) }

func operatorExitCode(class string) int {
	if class == "invalid_input" {
		return 2
	}
	if class == "precondition" {
		return 3
	}
	return 1
}

func writeOperatorResult(stdout io.Writer, operation, operationID string, success bool, exitClass, diagnostic string) int {
	result := operatorResult(operation, operationID, success, exitClass, diagnostic)
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return 1
	}
	if success {
		return 0
	}
	return operatorExitCode(exitClass)
}

func writeOperatorFailure(stdout io.Writer, operation, operationID, class string, code int, message string) int {
	_ = json.NewEncoder(stdout).Encode(operatorResult(operation, operationID, false, class, message))
	return code
}

func operatorResult(operation, operationID string, success bool, exitClass, diagnostic string) map[string]any {
	result := map[string]any{
		"schema": "database.operator.result/v1", "operation": operation, "operation_id": operationID,
		"success": success, "exit_class": exitClass,
	}
	if diagnostic != "" {
		result["diagnostic"] = diagnostic
	}
	return result
}

func version(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "Usage: database version [--format=human|json]")
		return 0
	}
	format, ok := formatFlag(args)
	if !ok {
		fmt.Fprintln(stderr, "database version: format must be human or json")
		return 2
	}
	info := buildinfo.Current()
	if format == "json" {
		if err := json.NewEncoder(stdout).Encode(info); err != nil {
			fmt.Fprintf(stderr, "database version: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "database %s\nbuild: %s\nplatform: %s\ngo: %s\ndata compatibility: %s\nbackup compatibility: %s\nMySQL profile: %s\n",
		info.ProductVersion, info.BuildIdentity, info.Platform, info.GoVersion,
		info.DataCompatibility, info.BackupCompatibility, info.MySQLApplicationCompatibilityProfile)
	return 0
}

func formatFlag(args []string) (string, bool) {
	format := "human"
	for i, argumentCount := 0, len(args); i < argumentCount; i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--format=") {
			format = strings.TrimPrefix(arg, "--format=")
		} else if arg == "--json" {
			format = "json"
		} else if arg == "--format" {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			format = args[i]
		} else {
			return "", false
		}
	}
	return format, format == "human" || format == "json"
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: database <command> [options]")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init      create a stopped database server instance")
	fmt.Fprintln(w, "  version   report the executable and compatibility identity")
	fmt.Fprintln(w, "  serve     run the process and optional diagnostics listener")
	fmt.Fprintln(w, "  config    validate the closed server configuration")
	fmt.Fprintln(w, "Use 'database <command> --help' for command-specific options.")
}
