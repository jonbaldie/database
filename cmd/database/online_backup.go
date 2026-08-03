package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func createOnlineBackup(request onlineConnectionRequest, output string, reporter *operationReporter) (map[string]any, error, string) {
	reporter.progress("connecting")
	password, err := request.readPassword(os.Stdin)
	if err != nil {
		return nil, err, "invalid_input"
	}
	db, err := request.openDatabase(password)
	if err != nil {
		return nil, errors.New("connection failed"), "access"
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, errors.New("connection failed"), "access"
	}
	if request.tlsMode == "disabled" {
		if warning := onlineNonLoopbackWarning(request.address); warning != nil {
			emitOnlineTLSWarning(reporter, warning)
		}
	}
	reporter.progress("capturing")
	files, err := captureOnlineBackupFiles(db)
	if err != nil {
		return nil, err, onlineBackupExitClass(err)
	}
	metadata, err := decodeBackupMetadata(files["instance.json"])
	if err != nil {
		return nil, errors.New("backup capture returned invalid instance metadata"), "operation_failed"
	}
	reporter.progress("writing")
	manifest := makeBackupManifest(metadata, files)
	if err := writeBackupArchive(output, manifest, files); err != nil {
		return nil, err, "operation_failed"
	}
	reporter.progress("validating")
	if err := inspectBackup(output); err != nil {
		_ = os.Remove(output)
		return nil, err, "operation_failed"
	}
	info, err := os.Stat(output)
	if err != nil {
		return nil, err, "operation_failed"
	}
	return map[string]any{
		"artifact_path":      filepath.Clean(output),
		"source_instance_id": metadata.InstanceID,
		"data_version":       effectiveDataVersion(metadata),
		"created_at":         manifest.CreatedAt,
		"backup_version":     manifest.BackupVersion,
		"size_bytes":         info.Size(),
		"complete":           true,
	}, nil, "success"
}

func captureOnlineBackupFiles(db *sql.DB) (map[string][]byte, error) {
	rows, err := db.Query("BACKUP INSTANCE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make(map[string][]byte)
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return nil, err
		}
		files[path] = []byte(content)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, ok := files["instance.json"]; !ok {
		return nil, errors.New("backup capture omitted instance metadata")
	}
	if _, ok := files["catalog.json"]; !ok {
		return nil, errors.New("backup capture omitted catalog")
	}
	return files, nil
}

func onlineBackupExitClass(err error) string {
	message := err.Error()
	if strings.Contains(message, "access denied") || strings.Contains(message, "Access denied") ||
		strings.Contains(message, "Error 1045") || strings.Contains(message, "Error 1227") {
		return "access"
	}
	return "operation_failed"
}

func emitOnlineTLSWarning(reporter *operationReporter, context map[string]string) {
	fmt.Fprintf(reporter.stderr, "%s [UNSAFE_NON_TLS_CONNECTION]: non-loopback backup connection without TLS (address=%s tls=disabled)\n",
		reporter.command, context["address"])
}
