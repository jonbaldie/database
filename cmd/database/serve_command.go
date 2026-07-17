package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"

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
	format, configurationArgs, err := configOutputFormat(args)
	if err != nil {
		return serveInputFailure(stderr, err)
	}
	opts, err := parseServeFlags(configurationArgs)
	if err != nil {
		return serveConfigurationFailure(stderr, err)
	}
	return serveLifecycle(format, opts, stdout, stderr)
}

func serveInputFailure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "database serve: invalid_input: %v\n", err)
	return 2
}

func serveConfigurationFailure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "database serve: %s: %v\n", configurationClass(err), err)
	return 2
}

func serveLifecycle(format string, opts lifecycle.Options, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	operationID := newOperationID()
	if err := lifecycle.Serve(ctx, opts, serveEventWriter(format, operationID, stdout)); err != nil {
		return serveFailure(format, operationID, stdout, stderr, err)
	}
	return serveSuccess(format, operationID, stdout)
}

func serveEventWriter(format, operationID string, stdout io.Writer) func(lifecycle.Event) {
	return func(event lifecycle.Event) {
		event.OperationID = operationID
		if format == "json" {
			_ = json.NewEncoder(stdout).Encode(event)
			return
		}
		writeHumanServeEvent(stdout, event)
	}
}

func writeHumanServeEvent(stdout io.Writer, event lifecycle.Event) {
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

func serveFailure(format, operationID string, stdout, stderr io.Writer, err error) int {
	if format == "json" {
		return writeOperatorFailure(stdout, "serve", operationID, "operation_failed", 1, err.Error())
	}
	fmt.Fprintf(stderr, "database serve: %v\n", err)
	return 1
}

func serveSuccess(format, operationID string, stdout io.Writer) int {
	if format == "json" {
		return writeOperatorResult(stdout, "serve", operationID, true, "success", "")
	}
	fmt.Fprintf(stdout, "database serve: success (operation_id=%s)\n", operationID)
	return 0
}
