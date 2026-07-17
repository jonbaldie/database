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
	case "backup", "restore", "upgrade", "data":
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
	if len(args) < 2 {
		return initFailure(stdout, "invalid_input", 2, "operator command requires an operation")
	}
	var err error
	switch args[0] + " " + args[1] {
	case "backup create":
		err = createBackup(option(args[2:], "--data-dir"), option(args[2:], "--output"))
	case "backup inspect":
		err = inspectBackup(option(args[2:], "--input"))
	case "restore":
		err = restoreBackup(option(args[2:], "--input"), option(args[2:], "--data-dir"))
	}
	if err != nil {
		_ = json.NewEncoder(stdout).Encode(map[string]any{"schema": "database.operator.result/v1", "operation": strings.Join(args[:2], " "), "operation_id": fmt.Sprintf("op-%d", time.Now().UnixNano()), "success": false, "exit_class": "operation_failed", "diagnostic": err.Error()})
		return 1
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{"schema": "database.operator.result/v1", "operation": strings.Join(args[:2], " "), "operation_id": fmt.Sprintf("op-%d", time.Now().UnixNano()), "success": true, "exit_class": "success"})
	return 0
}

func option(args []string, name string) string {
	for index, arg := range args {
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=")
		}
		if arg == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
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
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--password=") {
			return initFailure(stdout, "invalid_input", 2, "inline passwords are not supported")
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if !hasValue {
			switch name {
			case "--password-file":
				if i+1 >= len(args) {
					return initFailure(stdout, "invalid_input", 2, "--password-file requires a value")
				}
				i++
				value = args[i]
			case "--password-stdin":
				passwordStdin = true
				continue
			case "--format":
				if i+1 >= len(args) {
					return initFailure(stdout, "invalid_input", 2, "--format requires a value")
				}
				i++
				value = args[i]
			default:
				if strings.HasPrefix(arg, "-") {
					return initFailure(stdout, "invalid_input", 2, fmt.Sprintf("unknown flag %q", arg))
				}
				if directory != "" {
					return initFailure(stdout, "invalid_input", 2, "multiple data directories")
				}
				directory = arg
				continue
			}
		}
		switch name {
		case "--password-file":
			passwordFile = value
		case "--password-stdin":
			passwordStdin = true
		case "--format":
			format = value
		default:
			if directory != "" {
				return initFailure(stdout, "invalid_input", 2, "multiple data directories")
			}
			directory = name
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
	result := map[string]any{"schema": "database.operator.result/v1", "operation": "init", "success": true, "exit_class": "success", "instance_id": metadata.InstanceID, "data_directory": directory, "admin_account": metadata.AdminAccount}
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(result)
	} else {
		fmt.Fprintf(stdout, "initialized database instance %s\n", metadata.InstanceID)
	}
	return 0
}

func initFailure(stdout io.Writer, exitClass string, code int, message string) int {
	_ = json.NewEncoder(stdout).Encode(map[string]any{"schema": "database.operator.result/v1", "operation": "init", "success": false, "exit_class": exitClass, "diagnostic": message})
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
		fmt.Fprintln(stdout, "Usage: database serve [--config PATH] [--data-directory PATH] [--mysql-listen-address HOST:PORT] [--tls-certificate-file PATH --tls-private-key-file PATH] [--diagnostics-listen-address HOST:PORT] [--log-format=json|text]")
		fmt.Fprintln(stdout, "Compatibility aliases: --data-dir, --mysql-address, --tls-cert, --tls-key, --diagnostics-address, --format, --state-file")
		return 0
	}
	opts, err := parseServeFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "database serve: %s: %v\n", configurationClass(err), err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	emit := func(event lifecycle.Event) {
		if opts.Format == "json" {
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
		fmt.Fprintf(stderr, "database serve: %v\n", err)
		return 1
	}
	return 0
}

func configCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, "Usage: database config validate [--config PATH] [configuration flags]")
		return 0
	}
	if len(args) == 0 || args[0] != "validate" {
		return writeConfigFailure(stdout, "invalid_input", "config requires the validate operation")
	}
	config, err := resolveConfiguration(args[1:], os.Environ())
	if err != nil {
		return writeConfigFailure(stdout, configurationClass(err), err.Error())
	}
	result := configurationResult(config)
	result["operation"] = "config validate"
	result["success"] = true
	result["exit_class"] = "success"
	_ = json.NewEncoder(stdout).Encode(result)
	return 0
}

func writeConfigFailure(stdout io.Writer, class, message string) int {
	code := 1
	if class == "invalid_input" {
		code = 2
	} else if class == "precondition" {
		code = 3
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"schema": "database.operator.result/v1", "operation": "config validate",
		"success": false, "exit_class": class, "diagnostic": message,
	})
	return code
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
