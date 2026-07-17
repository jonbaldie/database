package main

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func backupRestoreCommand(args []string, stdout io.Writer) int {
	operation := operatorName(args)
	operationID := newOperationID()
	err, exitClass := executeBackupRestore(args)
	if err != nil {
		return writeOperatorFailure(stdout, operation, operationID, exitClass, operatorExitCode(exitClass), err.Error())
	}
	return writeOperatorResult(stdout, operation, operationID, true, "success", "")
}

func executeBackupRestore(args []string) (error, string) {
	if args[0] == "restore" {
		return restoreCommand(args[1:])
	}
	return backupCommand(args[1:])
}

func backupCommand(args []string) (error, string) {
	if len(args) == 0 {
		return errors.New("backup requires create or inspect"), "invalid_input"
	}
	if args[0] == "create" {
		return backupCreateCommand(args[1:])
	}
	if args[0] == "inspect" {
		return backupInspectCommand(args[1:])
	}
	return fmt.Errorf("unsupported backup operation %q", args[0]), "invalid_input"
}

func backupCreateCommand(args []string) (error, string) {
	options, err := operatorOptions(args, "--data-dir", "--output")
	if err != nil {
		return err, "invalid_input"
	}
	if options["--data-dir"] == "" || options["--output"] == "" {
		return errors.New("backup create requires --data-dir and --output"), "invalid_input"
	}
	return createBackup(options["--data-dir"], options["--output"]), "operation_failed"
}

func backupInspectCommand(args []string) (error, string) {
	options, err := operatorOptions(args, "--input")
	if err != nil {
		return err, "invalid_input"
	}
	if options["--input"] == "" {
		return errors.New("backup inspect requires --input"), "invalid_input"
	}
	return inspectBackup(options["--input"]), "operation_failed"
}

func restoreCommand(args []string) (error, string) {
	options, err := operatorOptions(args, "--input", "--data-dir")
	if err != nil {
		return err, "invalid_input"
	}
	if options["--input"] == "" || options["--data-dir"] == "" {
		return errors.New("restore requires --input and --data-dir"), "invalid_input"
	}
	return restoreBackup(options["--input"], options["--data-dir"]), "operation_failed"
}

func createBackup(directory, output string) error {
	if err := verifyBackupSource(directory); err != nil {
		return err
	}
	file, archive, err := createBackupArchive(output)
	if err != nil {
		return err
	}
	defer file.Close()
	defer archive.Close()
	return archiveDirectory(directory, archive)
}

func verifyBackupSource(directory string) error {
	if directory == "" {
		return errors.New("backup create requires --data-dir and --output")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return errors.New("data directory does not exist")
	}
	return nil
}

func createBackupArchive(output string) (*os.File, *tar.Writer, error) {
	if output == "" {
		return nil, nil, errors.New("backup create requires --data-dir and --output")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return file, tar.NewWriter(file), nil
}

func archiveDirectory(directory string, archive *tar.Writer) error {
	archiver := backupArchiver{directory: directory, archive: archive}
	return filepath.Walk(directory, archiver.writePath)
}

type backupArchiver struct {
	directory string
	archive   *tar.Writer
}

func (archiver backupArchiver) writePath(path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if path == archiver.directory {
		return nil
	}
	header, err := archiver.header(path, info)
	if err != nil {
		return err
	}
	if err := archiver.archive.WriteHeader(header); err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	return archiver.copyFile(path)
}

func (archiver backupArchiver) header(path string, info os.FileInfo) (*tar.Header, error) {
	relative, err := filepath.Rel(archiver.directory, path)
	if err != nil {
		return nil, err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return nil, err
	}
	header.Name = filepath.ToSlash(relative)
	return header, nil
}

func (archiver backupArchiver) copyFile(path string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	_, err = io.Copy(archiver.archive, input)
	return err
}

func inspectBackup(input string) error {
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := tar.NewReader(file).Next(); err != nil && !errors.Is(err, io.EOF) {
		return errors.New("invalid backup archive")
	}
	return nil
}

func restoreBackup(input, directory string) error {
	if err := prepareRestoreDestination(directory); err != nil {
		return err
	}
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	return extractBackup(tar.NewReader(file), directory)
}

func prepareRestoreDestination(directory string) error {
	if directory == "" {
		return errors.New("restore requires --input and --data-dir")
	}
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(directory, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("restore destination must be new or empty")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		return errors.New("restore destination must be new or empty")
	}
	return nil
}

func extractBackup(archive *tar.Reader, directory string) error {
	for {
		header, done, err := nextBackupEntry(archive)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if err := restoreEntry(archive, directory, header); err != nil {
			return err
		}
	}
}

func nextBackupEntry(archive *tar.Reader) (*tar.Header, bool, error) {
	header, err := archive.Next()
	if errors.Is(err, io.EOF) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, errors.New("invalid backup archive")
	}
	return header, false, nil
}

func restoreEntry(archive *tar.Reader, directory string, header *tar.Header) error {
	path, err := restorePath(directory, header.Name)
	if err != nil {
		return err
	}
	if header.FileInfo().IsDir() {
		return os.MkdirAll(path, 0o700)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return restoreFile(path, archive)
}

func restorePath(directory, name string) (string, error) {
	cleanName := filepath.Clean(name)
	if cleanName == "." || filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return "", errors.New("unsafe backup path")
	}
	return filepath.Join(directory, cleanName), nil
}

func restoreFile(path string, archive *tar.Reader) error {
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, archive)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
