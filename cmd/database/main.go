package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/buildinfo"
	"github.com/jonbaldie/database/internal/instance"
	"github.com/jonbaldie/database/internal/lifecycle"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version":
		return version(args[1:], stdout, stderr)
	case "init":
		return initialize(args[1:], stdout, stderr)
	case "backup", "restore", "upgrade", "data", "shutdown":
		return operatorCommand(args, stdout)
	case "config":
		return configCommand(args[1:], stdout, stderr)
	case "serve":
		return serve(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "database: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func operatorCommand(args []string, stdout io.Writer) int {
	operation := operatorName(args)
	operationID := newOperationID()
	if len(args) == 0 {
		return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, "operator command requires an operation")
	}

	var err error
	class := "operation_failed"
	switch args[0] {
	case "backup":
		if len(args) < 2 {
			return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, "backup requires create or inspect")
		}
		operation = strings.Join(args[:2], " ")
		switch args[1] {
		case "create":
			var options map[string]string
			options, err = operatorOptions(args[2:], "--data-dir", "--output")
			if err != nil {
				class = "invalid_input"
			}
			if err == nil {
				if options["--data-dir"] == "" || options["--output"] == "" {
					class = "invalid_input"
					err = errors.New("backup create requires --data-dir and --output")
				} else {
					err = createBackup(options["--data-dir"], options["--output"])
				}
			}
		case "inspect":
			var options map[string]string
			options, err = operatorOptions(args[2:], "--input")
			if err != nil {
				class = "invalid_input"
			}
			if err == nil {
				if options["--input"] == "" {
					class = "invalid_input"
					err = errors.New("backup inspect requires --input")
				} else {
					err = inspectBackup(options["--input"])
				}
			}
		default:
			return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, fmt.Sprintf("unsupported backup operation %q", args[1]))
		}
	case "restore":
		var options map[string]string
		options, err = operatorOptions(args[1:], "--input", "--data-dir")
		if err != nil {
			class = "invalid_input"
		}
		if err == nil {
			if options["--input"] == "" || options["--data-dir"] == "" {
				class = "invalid_input"
				err = errors.New("restore requires --input and --data-dir")
			} else {
				err = restoreBackup(options["--input"], options["--data-dir"])
			}
		}
	case "upgrade", "shutdown":
		if hasUnknownOperatorFlag(args[1:]) {
			class = "invalid_input"
			err = errors.New("unknown operator flag")
		} else {
			err = fmt.Errorf("%s is not implemented", args[0])
		}
	case "data":
		if len(args) < 2 {
			return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, "data requires validate or inspect")
		}
		operation = strings.Join(args[:2], " ")
		if args[1] != "validate" && args[1] != "inspect" {
			return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, fmt.Sprintf("unsupported data operation %q", args[1]))
		}
		if hasUnknownOperatorFlag(args[2:]) {
			class = "invalid_input"
			err = errors.New("unknown operator flag")
		} else {
			err = fmt.Errorf("%s is not implemented", operation)
		}
	default:
		return writeOperatorFailure(stdout, operation, operationID, "invalid_input", 2, fmt.Sprintf("unsupported operator command %q", args[0]))
	}
	if err != nil {
		return writeOperatorFailure(stdout, operation, operationID, class, operatorExitCode(class), err.Error())
	}
	return writeOperatorResult(stdout, operation, operationID, true, "success", "")
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
	for index := 0; index < len(args); index++ {
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
	result := map[string]any{
		"schema": "database.operator.result/v1", "operation": operation, "operation_id": operationID,
		"success": success, "exit_class": exitClass,
	}
	if diagnostic != "" {
		result["diagnostic"] = diagnostic
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return 1
	}
	if success {
		return 0
	}
	return operatorExitCode(exitClass)
}

func writeOperatorFailure(stdout io.Writer, operation, operationID, class string, code int, message string) int {
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"schema": "database.operator.result/v1", "operation": operation, "operation_id": operationID,
		"success": false, "exit_class": class, "diagnostic": message,
	})
	return code
}

func createBackup(directory, output string) error {
	if directory == "" || output == "" {
		return errors.New("backup create requires --data-dir and --output")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return errors.New("data directory does not exist")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	archive := tar.NewWriter(file)
	defer archive.Close()
	return filepath.Walk(directory, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(archive, input)
		return err
	})
}

func inspectBackup(input string) error {
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	archive := tar.NewReader(file)
	if _, err := archive.Next(); err != nil && !errors.Is(err, io.EOF) {
		return errors.New("invalid backup archive")
	}
	return nil
}

func restoreBackup(input, directory string) error {
	if input == "" || directory == "" {
		return errors.New("restore requires --input and --data-dir")
	}
	if _, err := os.Stat(directory); err == nil {
		entries, readErr := os.ReadDir(directory)
		if readErr != nil || len(entries) != 0 {
			return errors.New("restore destination must be new or empty")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	archive := tar.NewReader(file)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("invalid backup archive")
		}
		name := filepath.Clean(header.Name)
		if name == "." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return errors.New("unsafe backup path")
		}
		path := filepath.Join(directory, name)
		if header.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, archive)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func initialize(args []string, stdout, stderr io.Writer) int {
	directory := ""
	passwordFile := ""
	passwordStdin := false
	format := "human"
	formatSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			if directory != "" {
				return initFailure(stdout, "invalid_input", 2, "multiple data directories")
			}
			directory = arg
			continue
		}
		if strings.HasPrefix(arg, "--password=") {
			return initFailure(stdout, "invalid_input", 2, "inline passwords are not supported")
		}
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--password-file":
			if passwordFile != "" || passwordStdin {
				return initFailure(stdout, "invalid_input", 2, "password input may be specified once")
			}
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
					return initFailure(stdout, "invalid_input", 2, "--password-file requires a non-empty value")
				}
				i++
				value = args[i]
			}
			if value == "" {
				return initFailure(stdout, "invalid_input", 2, "--password-file requires a non-empty value")
			}
			passwordFile = value
		case "--password-stdin":
			if hasValue {
				return initFailure(stdout, "invalid_input", 2, "--password-stdin does not take a value")
			}
			if passwordFile != "" || passwordStdin {
				return initFailure(stdout, "invalid_input", 2, "password input may be specified once")
			}
			passwordStdin = true
		case "--format":
			if formatSet {
				return initFailure(stdout, "invalid_input", 2, "--format may be specified once")
			}
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
					return initFailure(stdout, "invalid_input", 2, "--format requires a value")
				}
				i++
				value = args[i]
			}
			if value == "" {
				return initFailure(stdout, "invalid_input", 2, "--format requires a value")
			}
			formatSet = true
			format = value
		default:
			return initFailure(stdout, "invalid_input", 2, fmt.Sprintf("unknown flag %q", name))
		}
	}
	if directory == "" || (passwordFile == "" && !passwordStdin) || (passwordFile != "" && passwordStdin) || (format != "human" && format != "json") {
		return initFailure(stdout, "invalid_input", 2, "usage: database init DIRECTORY (--password-file FILE | --password-stdin) [--format=human|json]")
	}
	if info, err := os.Stat(directory); err == nil {
		if info.IsDir() {
			entries, readErr := os.ReadDir(directory)
			if readErr == nil && len(entries) != 0 {
				return initFailure(stdout, "precondition", 3, "data directory is not empty")
			}
		}
	}
	password, err := instance.ReadPassword(passwordFile, os.Stdin)
	if err != nil {
		return initFailure(stdout, "invalid_input", 2, "unable to read password")
	}
	metadata, err := instance.Initialize(directory, "admin", password)
	if err != nil {
		return initFailure(stdout, "precondition", 3, err.Error())
	}
	result := map[string]any{"schema": "database.operator.result/v1", "operation": "init", "operation_id": newOperationID(), "success": true, "exit_class": "success", "instance_id": metadata.InstanceID, "data_directory": directory, "admin_account": metadata.AdminAccount}
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(result)
	} else {
		fmt.Fprintf(stdout, "initialized database instance %s\n", metadata.InstanceID)
	}
	return 0
}

func initFailure(stdout io.Writer, exitClass string, code int, message string) int {
	_ = json.NewEncoder(stdout).Encode(map[string]any{"schema": "database.operator.result/v1", "operation": "init", "operation_id": newOperationID(), "success": false, "exit_class": exitClass, "diagnostic": message})
	return code
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

func serve(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "Usage: database serve [--format=human|json] [--config PATH] [--data-directory PATH] [--mysql-listen-address HOST:PORT] [--tls-certificate-file PATH --tls-private-key-file PATH] [--diagnostics-listen-address HOST:PORT] [--log-format=json|text]")
		return 0
	}
	outputFormat, configurationArgs, err := configOutputFormat(args)
	if err != nil {
		fmt.Fprintf(stderr, "database serve: invalid_input: %v\n", err)
		return 2
	}
	opts, err := parseServeFlags(configurationArgs)
	if err != nil {
		fmt.Fprintf(stderr, "database serve: %s: %v\n", configurationClass(err), err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	operationID := newOperationID()
	emit := func(event lifecycle.Event) {
		event.OperationID = operationID
		if outputFormat == "json" {
			_ = json.NewEncoder(stdout).Encode(event)
			return
		}
		if event.State == "ready" {
			if event.DiagnosticsAddress == "" {
				fmt.Fprintln(stdout, "database: ready")
			} else {
				fmt.Fprintf(stdout, "database: ready (diagnostics=%s)\n", event.DiagnosticsAddress)
			}
		} else {
			fmt.Fprintf(stdout, "database: %s\n", event.State)
		}
	}
	if err := lifecycle.Serve(ctx, opts, emit); err != nil {
		if outputFormat == "json" {
			return writeOperatorFailure(stdout, "serve", operationID, "operation_failed", 1, err.Error())
		}
		fmt.Fprintf(stderr, "database serve: %v\n", err)
		return 1
	}
	if outputFormat == "json" {
		return writeOperatorResult(stdout, "serve", operationID, true, "success", "")
	}
	fmt.Fprintf(stdout, "database serve: success (operation_id=%s)\n", operationID)
	return 0
}

func configCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "Usage: database config validate [--config PATH] [configuration flags]")
		return 0
	}
	format, parsedArgs, err := configOutputFormat(args)
	operationID := newOperationID()
	if err != nil {
		return writeConfigFailure(stdout, "invalid_input", err.Error(), "config validate", operationID, format)
	}
	if len(parsedArgs) == 0 || parsedArgs[0] != "validate" {
		operation := "config"
		if len(parsedArgs) > 0 {
			operation += " " + parsedArgs[0]
		}
		return writeConfigFailure(stdout, "invalid_input", "config requires the validate operation", operation, operationID, format)
	}
	configurationArgs := parsedArgs[1:]
	config, err := resolveConfiguration(configurationArgs, os.Environ())
	if err != nil {
		return writeConfigFailure(stdout, configurationClass(err), err.Error(), "config validate", operationID, format)
	}
	result := configurationResult(config, operationID)
	result["operation"] = "config validate"
	result["success"] = true
	result["exit_class"] = "success"
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(result)
	} else {
		writeConfigurationHuman(stdout, config, operationID)
	}
	return 0
}

func configOutputFormat(args []string) (string, []string, error) {
	format := "human"
	seen := false
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg != "--format" && !strings.HasPrefix(arg, "--format=") {
			filtered = append(filtered, arg)
			continue
		}
		if seen {
			return format, nil, errors.New("repeated output format")
		}
		seen = true
		value := "json"
		if arg == "--format" {
			if index+1 >= len(args) {
				return format, nil, errors.New("--format requires a value")
			}
			index++
			value = args[index]
		} else if strings.HasPrefix(arg, "--format=") {
			value = strings.TrimPrefix(arg, "--format=")
		}
		switch value {
		case "json":
			format = "json"
		case "human", "text":
			format = "human"
		default:
			return format, nil, fmt.Errorf("format must be human, text, or json")
		}
	}
	return format, filtered, nil
}

func writeConfigFailure(stdout io.Writer, class, message, operation, operationID, format string) int {
	if format != "json" {
		fmt.Fprintf(stdout, "configuration invalid [%s] (operation_id=%s): %s\n", class, operationID, message)
		return operatorExitCode(class)
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"schema": "database.operator.result/v1", "operation": operation, "operation_id": operationID,
		"success": false, "exit_class": class, "diagnostic": message,
	})
	return operatorExitCode(class)
}

func writeConfigurationHuman(stdout io.Writer, config configuration, operationID string) {
	fmt.Fprintf(stdout, "configuration valid (operation_id=%s)\n", operationID)
	names := make([]string, 0, len(config.values))
	for name := range config.values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		setting := config.values[name]
		value := setting.value
		if name == "tls_private_key_file" && value != "" {
			value = "[redacted]"
		}
		fmt.Fprintf(stdout, "%s=%s (%s)\n", name, value, setting.source)
	}
}

func formatFlag(args []string) (string, bool) {
	format := "human"
	for i := 0; i < len(args); i++ {
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
