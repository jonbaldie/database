package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
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
	db, err, exitClass := connectOnlineCommand(request.onlineConnectionRequest, reporter)
	if err != nil {
		return nil, err, exitClass
	}
	defer db.Close()
	reporter.progress("requesting")
	details, err := requestServerShutdown(db, reporter.id)
	if err != nil {
		return nil, errors.New("shutdown request failed"), onlineAccessExitClass(err)
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

func requestServerShutdown(db *sql.DB, operationID string) (map[string]any, error) {
	rows, err := db.Query("SHUTDOWN '" + escapeSQLString(operationID) + "'")
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

func escapeSQLString(value string) string {
	replaced := make([]byte, 0, len(value))
	for _, character := range []byte(value) {
		if character == '\'' {
			replaced = append(replaced, '\'', '\'')
			continue
		}
		replaced = append(replaced, character)
	}
	return string(replaced)
}

func waitForOnlineShutdown(address string) error {
	deadline := time.Now().Add(30 * time.Second)
	failures := 0
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err != nil {
			failures++
			if failures >= 3 {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		failures = 0
		_ = connection.Close()
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("server did not stop")
}
