package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

type shutdownRequest struct {
	onlineConnectionRequest
	yes bool
}

func shutdownCommand(args []string, stdout, stderr io.Writer) int {
	output, filtered, err := parseCommandOutput(args, true)
	if err != nil {
		return newOperationReporter("shutdown", commandOutput{result: "json", progress: "none"}, stdout, stderr).failure("invalid_input", "", err.Error(), nil)
	}
	if !containsOutputControl(args) {
		output.result = "json"
		output.legacy = true
	}
	reporter := newOperationReporter("shutdown", output, stdout, stderr)
	details, err, exitClass := executeShutdown(filtered, reporter)
	if err != nil {
		return reporter.failure(exitClass, "", err.Error(), details)
	}
	return reporter.success(details)
}

func executeShutdown(args []string, reporter *operationReporter) (map[string]any, error, string) {
	request, err := parseShutdownRequest(args)
	if err != nil {
		return nil, err, "invalid_input"
	}
	return performShutdown(request, reporter)
}

func parseShutdownRequest(args []string) (shutdownRequest, error) {
	online, remaining, err := parseOnlineConnectionRequest(args)
	if err != nil {
		return shutdownRequest{}, err
	}
	request := shutdownRequest{onlineConnectionRequest: online}
	for _, arg := range remaining {
		if arg != "--yes" {
			return shutdownRequest{}, fmt.Errorf("unknown flag %q", arg)
		}
		if request.yes {
			return shutdownRequest{}, errors.New("--yes may be specified once")
		}
		request.yes = true
	}
	if !request.yes {
		return shutdownRequest{}, errors.New("shutdown requires --yes for non-interactive use")
	}
	return request, nil
}

func performShutdown(request shutdownRequest, reporter *operationReporter) (map[string]any, error, string) {
	db, err, exitClass := connectOnlineShutdown(request, reporter)
	if err != nil {
		return nil, err, exitClass
	}
	defer db.Close()
	reporter.progress("requesting")
	details, err := requestServerShutdown(db)
	if err != nil {
		return nil, errors.New("shutdown request failed"), shutdownExitClass(err)
	}
	reporter.progress("draining")
	_ = db.Close()
	if err := waitForOnlineShutdown(request.address); err != nil {
		return details, err, "operation_failed"
	}
	reporter.progress("stopped")
	details["state"] = "stopped"
	details["stopping_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	return details, nil, "success"
}

func connectOnlineShutdown(request shutdownRequest, reporter *operationReporter) (*sql.DB, error, string) {
	reporter.progress("connecting")
	password, err := readOnlinePassword(request.onlineConnectionRequest, os.Stdin)
	if err != nil {
		return nil, err, "invalid_input"
	}
	db, err := openOnlineDatabase(request.onlineConnectionRequest, password)
	if err != nil {
		return nil, errors.New("connection failed"), "access"
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, errors.New("connection failed"), "access"
	}
	if request.tlsMode == "disabled" {
		if warning := onlineNonLoopbackWarning(request.address); warning != nil {
			emitShutdownTLSWarning(reporter, warning)
		}
	}
	return db, nil, "success"
}

func requestServerShutdown(db *sql.DB) (map[string]any, error) {
	rows, err := db.Query("SHUTDOWN")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("shutdown returned no instance identity")
	}
	var instanceID, requestedAt string
	if err := rows.Scan(&instanceID, &requestedAt); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"instance_id":  instanceID,
		"requested_at": requestedAt,
		"state":        "stopping",
	}, nil
}

func waitForOnlineShutdown(address string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err != nil {
			return nil
		}
		_ = connection.Close()
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("server did not stop")
}

func shutdownExitClass(err error) string {
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1045 || mysqlError.Number == 1227) {
		return "access"
	}
	return "operation_failed"
}

func emitShutdownTLSWarning(reporter *operationReporter, context map[string]string) {
	fmt.Fprintf(reporter.stderr, "%s [UNSAFE_NON_TLS_CONNECTION]: non-loopback shutdown connection without TLS (address=%s tls=disabled)\n",
		reporter.command, context["address"])
}
