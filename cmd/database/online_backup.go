package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/jonbaldie/database/internal/instance"
)

func createOnlineBackup(request onlineConnectionRequest, output string, reporter *operationReporter) (map[string]any, error, string) {
	if err := rejectExistingBackupOutput(output); err != nil {
		return nil, err, "precondition"
	}
	db, err, exitClass := connectOnlineBackup(request, reporter)
	if err != nil {
		return nil, err, exitClass
	}
	defer db.Close()
	reporter.progress("capturing")
	files, err := captureOnlineBackupFiles(db)
	if err != nil {
		return nil, err, onlineBackupExitClass(err)
	}
	defer files.Close()
	return writeValidatedOnlineBackup(files, output, reporter)
}

func rejectExistingBackupOutput(output string) error {
	if _, err := os.Stat(output); err == nil {
		return errors.New("backup output must be a new path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func connectOnlineBackup(request onlineConnectionRequest, reporter *operationReporter) (*sql.DB, error, string) {
	return connectOnlineCommand(request, reporter)
}

func connectOnlineCommand(request onlineConnectionRequest, reporter *operationReporter) (*sql.DB, error, string) {
	reporter.progress("connecting")
	password, err := readOnlinePassword(request, os.Stdin)
	if err != nil {
		return nil, err, "invalid_input"
	}
	db, err := openOnlineDatabase(request, password)
	if err != nil {
		return nil, errors.New("connection failed"), "access"
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, errors.New("connection failed"), "access"
	}
	warnOnlineNonTLS(request, reporter)
	return db, nil, "success"
}

func warnOnlineNonTLS(request onlineConnectionRequest, reporter *operationReporter) {
	if request.tlsMode != "disabled" {
		return
	}
	if warning := onlineNonLoopbackWarning(request.address); warning != nil {
		emitOnlineTLSWarning(reporter, warning)
	}
}

func writeValidatedOnlineBackup(capture onlineBackupCapture, output string, reporter *operationReporter) (map[string]any, error, string) {
	reporter.progress("writing")
	manifest := makeSourceBackupManifest(capture.metadata, capture.files)
	if err := writeSourceBackupArchive(output, manifest, capture.files); err != nil {
		return nil, err, "operation_failed"
	}
	reporter.progress("validating")
	if _, err := inspectBackup(output); err != nil {
		_ = os.Remove(output)
		return nil, err, "operation_failed"
	}
	return onlineBackupDetails(output, capture.metadata, manifest)
}

func onlineBackupDetails(output string, metadata instance.Metadata, manifest backupManifest) (map[string]any, error, string) {
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

type onlineBackupCapture struct {
	directory string
	files     []sourceBackupFile
	metadata  instance.Metadata
}

func (capture onlineBackupCapture) Close() error {
	if capture.directory == "" {
		return nil
	}
	return os.RemoveAll(capture.directory)
}

func captureOnlineBackupFiles(db *sql.DB) (onlineBackupCapture, error) {
	rows, err := db.Query("BACKUP INSTANCE")
	if err != nil {
		return onlineBackupCapture{}, err
	}
	return captureOnlineBackupRows(rows)
}

type onlineBackupRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

func captureOnlineBackupRows(rows onlineBackupRows) (onlineBackupCapture, error) {
	defer rows.Close()
	directory, err := os.MkdirTemp("", ".database-online-backup-*")
	if err != nil {
		return onlineBackupCapture{}, err
	}
	capture := onlineBackupCapture{directory: directory}
	if err := capture.readRows(rows); err != nil {
		_ = capture.Close()
		return onlineBackupCapture{}, err
	}
	files, err := readSourceFiles(directory)
	if err != nil {
		_ = capture.Close()
		return onlineBackupCapture{}, err
	}
	capture.files = files
	capture.metadata, err = decodeBackupMetadataFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		_ = capture.Close()
		return onlineBackupCapture{}, errors.New("backup capture returned invalid instance metadata")
	}
	return capture, nil
}

func (capture *onlineBackupCapture) readRows(rows onlineBackupRows) error {
	writers := make(map[string]*os.File, 2)
	defer closeOnlineBackupWriters(writers)
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return err
		}
		if err := capture.writeRow(writers, path, content); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return finishOnlineBackupWriters(writers)
}

func (capture *onlineBackupCapture) writeRow(writers map[string]*os.File, path, content string) error {
	writer, err := capture.writer(writers, path)
	if err != nil {
		return err
	}
	_, err = writer.WriteString(content)
	return err
}

func finishOnlineBackupWriters(writers map[string]*os.File) error {
	for _, path := range []string{"instance.json", "catalog.json"} {
		writer, found := writers[path]
		if !found {
			if path == "instance.json" {
				return errors.New("backup capture omitted instance metadata")
			}
			return errors.New("backup capture omitted catalog")
		}
		if err := writer.Sync(); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
		delete(writers, path)
	}
	return nil
}

func (capture *onlineBackupCapture) writer(writers map[string]*os.File, path string) (*os.File, error) {
	if path != "instance.json" && path != "catalog.json" {
		return nil, fmt.Errorf("backup capture returned unexpected path %q", path)
	}
	if writer := writers[path]; writer != nil {
		return writer, nil
	}
	writer, err := os.OpenFile(filepath.Join(capture.directory, path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	writers[path] = writer
	return writer, nil
}

func closeOnlineBackupWriters(writers map[string]*os.File) {
	for _, writer := range writers {
		_ = writer.Close()
	}
}

func onlineBackupExitClass(err error) string {
	return onlineAccessExitClass(err)
}

func onlineAccessExitClass(err error) string {
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && (mysqlError.Number == 1045 || mysqlError.Number == 1227) {
		return "access"
	}
	return "operation_failed"
}

func emitOnlineTLSWarning(reporter *operationReporter, context map[string]string) {
	fmt.Fprintf(reporter.stderr, "%s [UNSAFE_NON_TLS_CONNECTION]: non-loopback online connection without TLS (address=%s tls=disabled)\n",
		reporter.command, context["address"])
}
