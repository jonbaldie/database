package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/jonbaldie/database/internal/buildinfo"
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
		fmt.Fprintln(stdout, "Usage: database serve [--format=human|json] [--diagnostics-address HOST:PORT] [--state-file PATH]")
		return 0
	}
	opts, err := parseServeFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "database serve: %v\n", err)
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

func parseServeFlags(args []string) (lifecycle.Options, error) {
	opts := lifecycle.Options{Format: "human"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" {
			opts.Format = "json"
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if !hasValue {
			switch name {
			case "--format", "--diagnostics-address", "--state-file":
				if i+1 >= len(args) {
					return opts, fmt.Errorf("%s requires a value", name)
				}
				i++
				value = args[i]
			default:
				return opts, fmt.Errorf("unknown flag %q", arg)
			}
		}
		switch name {
		case "--format":
			opts.Format = value
		case "--diagnostics-address":
			opts.DiagnosticsAddress = value
		case "--state-file":
			opts.StateFile = value
		default:
			return opts, fmt.Errorf("unknown flag %q", name)
		}
	}
	if opts.Format != "human" && opts.Format != "json" {
		return opts, errors.New("format must be human or json")
	}
	return opts, nil
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: database <command> [options]")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  version   report the executable and compatibility identity")
	fmt.Fprintln(w, "  serve     run the process and optional diagnostics listener")
	fmt.Fprintln(w, "Use 'database <command> --help' for command-specific options.")
}
