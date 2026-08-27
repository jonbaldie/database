package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jonbaldie/database/internal/buildinfo"
	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/instance"
)

const (
	backupManifestPath = "backup.json"
	backupSchema       = "database.backup/v1"
)

type backupManifest struct {
	Schema            string       `json:"schema"`
	BackupVersion     string       `json:"backup_version"`
	DataCompatibility string       `json:"data_compatibility"`
	DataVersion       string       `json:"data_version,omitempty"`
	SourceInstanceID  string       `json:"source_instance_id"`
	CreatedAt         string       `json:"created_at"`
	Complete          bool         `json:"complete"`
	Files             []backupFile `json:"files"`
}

type backupFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type backupArchive struct {
	manifest  backupManifest
	files     map[string]storedBackupFile
	directory string
}

type storedBackupFile struct {
	path   string
	size   int64
	sha256 string
}

type sourceBackupFile struct {
	backupFile
	source string
}

func backupRestoreCommand(args []string, stdout, stderr io.Writer) int {
	operation := operatorName(args)
	output, filtered, err := parseCommandOutput(args, true)
	if err != nil {
		return newOperationReporter(operation, commandOutput{result: "json", progress: "none"}, stdout, stderr).failure("invalid_input", "", err.Error(), nil)
	}
	// Keep the original machine-readable output for callers that did not opt
	// into the v1 human default. Explicit --result always wins.
	if !containsOutputControl(args) {
		output.result = "json"
		output.legacy = true
	}
	reporter := newOperationReporter(operation, output, stdout, stderr)
	details, err, exitClass := executeBackupRestore(filtered, reporter)
	if err != nil {
		if reporter.output.legacy && strings.HasPrefix(operation, "backup inspect") && exitClass == "invalid_artifact" {
			exitClass = "operation_failed"
		}
		return reporter.failure(exitClass, "", err.Error(), nil)
	}
	return reporter.success(details)
}

func executeBackupRestore(args []string, reporter *operationReporter) (map[string]any, error, string) {
	if len(args) == 0 {
		return nil, errors.New("backup or restore requires an operation"), "invalid_input"
	}
	if args[0] == "restore" {
		reportBackupRestoreProgress(reporter, "restore")
		err, exitClass := restoreCommand(args[1:])
		return nil, err, exitClass
	}
	details, err, exitClass := backupCommand(args[1:], reporter)
	if err != nil && len(args) > 1 && args[1] == "inspect" && exitClass == "operation_failed" {
		exitClass = "invalid_artifact"
	}
	return details, err, exitClass
}

func reportBackupRestoreProgress(reporter *operationReporter, operation string) {
	progress := map[string][]string{
		"backup inspect": {"reading", "validating"},
		"restore":        {"preflight", "restoring", "validating"},
	}[operation]
	for _, phase := range progress {
		reporter.progress(phase)
	}
}

func backupCommand(args []string, reporter *operationReporter) (map[string]any, error, string) {
	if len(args) == 0 {
		return nil, errors.New("backup requires create or inspect"), "invalid_input"
	}
	if args[0] == "create" {
		return backupCreateCommand(args[1:], reporter)
	}
	if args[0] == "inspect" {
		reportBackupRestoreProgress(reporter, "backup inspect")
		err, exitClass := backupInspectCommand(args[1:])
		return nil, err, exitClass
	}
	return nil, fmt.Errorf("unsupported backup operation %q", args[0]), "invalid_input"
}

func backupCreateCommand(args []string, reporter *operationReporter) (map[string]any, error, string) {
	request, remaining, err := parseOnlineConnectionRequest(args)
	if err != nil {
		return nil, err, "invalid_input"
	}
	options, err := operatorOptions(remaining, "--output")
	if err != nil {
		return nil, err, "invalid_input"
	}
	if options["--output"] == "" {
		return nil, errors.New("backup create requires --output"), "invalid_input"
	}
	return createOnlineBackup(request, options["--output"], reporter)
}

func backupInspectCommand(args []string) (error, string) {
	options, err := operatorOptions(args, "--input", "--backup")
	if err != nil {
		return err, "invalid_input"
	}
	if options["--input"] != "" && options["--backup"] != "" {
		return errors.New("backup inspect input may be specified once"), "invalid_input"
	}
	if options["--input"] == "" {
		options["--input"] = options["--backup"]
	}
	if options["--input"] == "" {
		return errors.New("backup inspect requires --backup"), "invalid_input"
	}
	return inspectBackup(options["--input"]), "operation_failed"
}

func restoreCommand(args []string) (error, string) {
	options, err := operatorOptions(args, "--input", "--backup", "--data-dir", "--data-directory")
	if err != nil {
		return err, "invalid_input"
	}
	if err := normalizeRestoreOptions(options); err != nil {
		return err, "invalid_input"
	}
	return restoreBackup(options["--input"], options["--data-dir"]), "operation_failed"
}

func normalizeRestoreOptions(options map[string]string) error {
	if options["--input"] != "" && options["--backup"] != "" {
		return errors.New("restore backup may be specified once")
	}
	if options["--data-dir"] != "" && options["--data-directory"] != "" {
		return errors.New("restore data directory may be specified once")
	}
	if options["--input"] == "" {
		options["--input"] = options["--backup"]
	}
	if options["--data-dir"] == "" {
		options["--data-dir"] = options["--data-directory"]
	}
	if options["--input"] == "" || options["--data-dir"] == "" {
		return errors.New("restore requires --input and --data-dir")
	}
	return nil
}

func createBackup(directory, output string) error {
	if err := verifyBackupSource(directory); err != nil {
		return err
	}
	if err := rejectPathOverlap(directory, output); err != nil {
		return err
	}
	files, metadata, err := captureBackupSource(directory)
	if err != nil {
		return err
	}
	manifest := makeSourceBackupManifest(metadata, files)
	return writeSourceBackupArchive(output, manifest, files)
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

func captureBackupSource(directory string) ([]sourceBackupFile, instance.Metadata, error) {
	metadata, err := instance.Load(directory)
	if err != nil {
		return nil, instance.Metadata{}, fmt.Errorf("invalid source instance: %w", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "catalog.json")); err != nil {
		return nil, instance.Metadata{}, fmt.Errorf("invalid source catalog: %w", err)
	}
	if _, err := catalog.Open(directory); err != nil {
		return nil, instance.Metadata{}, fmt.Errorf("invalid source catalog: %w", err)
	}
	files, err := readSourceFiles(directory)
	if err != nil {
		return nil, instance.Metadata{}, err
	}
	paths := make(map[string]bool, len(files))
	for _, file := range files {
		paths[file.Path] = true
	}
	if !paths["instance.json"] {
		return nil, instance.Metadata{}, errors.New("source instance metadata is missing")
	}
	if !paths["catalog.json"] {
		return nil, instance.Metadata{}, errors.New("source catalog is missing")
	}
	return files, metadata, nil
}

func readSourceFiles(directory string) ([]sourceBackupFile, error) {
	collector := sourceFileCollector{directory: directory}
	err := filepath.Walk(directory, collector.visit)
	return collector.files, err
}

type sourceFileCollector struct {
	directory string
	files     []sourceBackupFile
}

func (collector *sourceFileCollector) visit(path string, info os.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if path == collector.directory {
		return nil
	}
	relative, err := filepath.Rel(collector.directory, path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if runtimeSourcePath(filepath.ToSlash(relative)) {
			return filepath.SkipDir
		}
		return nil
	}
	return collector.capture(relative, path, info)
}

func (collector *sourceFileCollector) capture(relative, path string, info os.FileInfo) error {
	relative = filepath.ToSlash(relative)
	if runtimeSourcePath(relative) {
		return nil
	}
	if relative == backupManifestPath {
		return errors.New("source entry uses a reserved backup path")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported source entry %q", relative)
	}
	digest, err := hashBackupFile(path)
	if err != nil {
		return err
	}
	collector.files = append(collector.files, sourceBackupFile{
		backupFile: backupFile{Path: relative, Size: info.Size(), SHA256: digest},
		source:     path,
	})
	return nil
}

func hashBackupFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, file, make([]byte, 64*1024)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func runtimeSourcePath(path string) bool {
	base := filepath.Base(path)
	return base == ".running.lock" || base == ".database-state" || base == ".database-initializing" || base == instance.UpgradeIncompleteMarker || strings.HasPrefix(base, ".catalog-") && strings.HasSuffix(base, ".tmp")
}

func effectiveDataVersion(metadata instance.Metadata) string {
	if metadata.DataVersion == "" {
		return instance.CurrentDataVersion
	}
	return metadata.DataVersion
}

func makeBackupManifest(metadata instance.Metadata, files map[string][]byte) backupManifest {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	entries := make([]backupFile, 0, len(paths))
	for _, path := range paths {
		contents := files[path]
		digest := sha256.Sum256(contents)
		entries = append(entries, backupFile{Path: path, Size: int64(len(contents)), SHA256: hex.EncodeToString(digest[:])})
	}
	return newBackupManifest(metadata, entries)
}

func makeSourceBackupManifest(metadata instance.Metadata, files []sourceBackupFile) backupManifest {
	entries := make([]backupFile, len(files))
	for index, file := range files {
		entries[index] = file.backupFile
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })
	return newBackupManifest(metadata, entries)
}

func newBackupManifest(metadata instance.Metadata, entries []backupFile) backupManifest {
	info := buildinfo.Current()
	return backupManifest{Schema: backupSchema, BackupVersion: info.BackupCompatibility,
		DataCompatibility: info.DataCompatibility, DataVersion: effectiveDataVersion(metadata), SourceInstanceID: metadata.InstanceID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Complete: true, Files: entries}
}

func writeBackupArchive(output string, manifest backupManifest, files map[string][]byte) error {
	return writeTemporaryBackupArchive(output, func(file *os.File) error {
		return writeBackupEntries(file, manifest, files)
	})
}

func writeSourceBackupArchive(output string, manifest backupManifest, files []sourceBackupFile) error {
	return writeTemporaryBackupArchive(output, func(file *os.File) error {
		return writeSourceBackupEntries(file, manifest, files)
	})
}

func writeTemporaryBackupArchive(output string, write func(*os.File) error) error {
	temporary, err := os.CreateTemp(filepath.Dir(output), ".database-backup-*")
	if err != nil {
		return err
	}
	if err := write(temporary); err != nil {
		return failTemporaryArchive(temporary, err)
	}
	if err := syncTemporaryArchive(temporary); err != nil {
		return failTemporaryArchive(temporary, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	if err := os.Rename(temporary.Name(), output); err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	return nil
}

func writeSourceBackupEntries(file *os.File, manifest backupManifest, files []sourceBackupFile) error {
	archive := tar.NewWriter(file)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		_ = archive.Close()
		return err
	}
	if err := writeArchiveFile(archive, backupManifestPath, manifestBytes); err != nil {
		_ = archive.Close()
		return err
	}
	ordered := append([]sourceBackupFile(nil), files...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Path < ordered[right].Path })
	buffer := make([]byte, 64*1024)
	for _, entry := range ordered {
		if err := writeSourceArchiveFile(archive, entry, buffer); err != nil {
			_ = archive.Close()
			return err
		}
	}
	return archive.Close()
}

func writeSourceArchiveFile(archive *tar.Writer, entry sourceBackupFile, buffer []byte) error {
	file, err := os.Open(entry.source)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := archive.WriteHeader(&tar.Header{Name: entry.Path, Mode: 0o600, Size: entry.Size}); err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(archive, hash), file, buffer)
	if err != nil {
		return err
	}
	if written != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("source file changed during backup: %s", entry.Path)
	}
	return nil
}

func writeBackupEntries(file *os.File, manifest backupManifest, files map[string][]byte) error {
	archive := tar.NewWriter(file)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		_ = archive.Close()
		return err
	}
	if err := writeArchiveFile(archive, backupManifestPath, manifestBytes); err != nil {
		_ = archive.Close()
		return err
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := writeArchiveFile(archive, path, files[path]); err != nil {
			_ = archive.Close()
			return err
		}
	}
	return archive.Close()
}

func syncTemporaryArchive(file *os.File) error {
	return file.Sync()
}

func closeTemporaryArchive(file *os.File) error {
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func failTemporaryArchive(file *os.File, primary error) error {
	_ = closeTemporaryArchive(file)
	return primary
}

func writeArchiveFile(archive *tar.Writer, path string, contents []byte) error {
	if err := archive.WriteHeader(&tar.Header{Name: path, Mode: 0o600, Size: int64(len(contents))}); err != nil {
		return err
	}
	_, err := archive.Write(contents)
	return err
}

func rejectPathOverlap(source, target string) error {
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	targetPath, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(sourcePath, targetPath)
	if err != nil {
		return err
	}
	if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("backup output must be outside the source directory")
	}
	return nil
}

func inspectBackup(input string) error {
	archive, err := loadBackupArchive(input)
	if err == nil {
		err = archive.Close()
	}
	return err
}

func loadBackupArchive(input string) (backupArchive, error) {
	file, err := os.Open(input)
	if err != nil {
		return backupArchive{}, err
	}
	defer file.Close()
	directory, err := os.MkdirTemp("", ".database-backup-read-*")
	if err != nil {
		return backupArchive{}, err
	}
	manifest, files, err := readBackupEntries(tar.NewReader(file), directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return backupArchive{}, err
	}
	if err := validateBackupArchive(manifest, files); err != nil {
		_ = os.RemoveAll(directory)
		return backupArchive{}, err
	}
	return backupArchive{manifest: manifest, files: files, directory: directory}, nil
}

func (archive backupArchive) Close() error {
	if archive.directory == "" {
		return nil
	}
	return os.RemoveAll(archive.directory)
}

func readBackupEntries(archive *tar.Reader, directory string) (backupManifest, map[string]storedBackupFile, error) {
	files := make(map[string]storedBackupFile)
	seen := make(map[string]bool)
	var manifest backupManifest
	manifestFound := false
	buffer := make([]byte, 64*1024)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if !manifestFound {
				return backupManifest{}, nil, errors.New("backup manifest is missing")
			}
			return manifest, files, nil
		}
		if err != nil {
			return backupManifest{}, nil, errors.New("invalid backup archive")
		}
		path, err := restorePath("", header.Name)
		if err != nil || path == "" {
			return backupManifest{}, nil, errors.New("unsafe backup path")
		}
		if header.FileInfo().IsDir() {
			continue
		}
		if !header.FileInfo().Mode().IsRegular() || header.Size < 0 {
			return backupManifest{}, nil, errors.New("invalid backup archive entry")
		}
		if seen[path] {
			return backupManifest{}, nil, errors.New("duplicate backup archive entry")
		}
		seen[path] = true
		if path == backupManifestPath {
			if manifestFound || decodeBackupManifest(archive, header.Size, &manifest) != nil {
				return backupManifest{}, nil, errors.New("invalid backup manifest")
			}
			manifestFound = true
			continue
		}
		stored, err := extractBackupEntry(archive, directory, path, header.Size, buffer)
		if err != nil {
			return backupManifest{}, nil, err
		}
		files[path] = stored
	}
}

func decodeBackupManifest(archive io.Reader, size int64, manifest *backupManifest) error {
	decoder := json.NewDecoder(io.LimitReader(archive, size))
	if err := decoder.Decode(manifest); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("backup manifest has trailing data")
	}
	return nil
}

func extractBackupEntry(archive io.Reader, directory, path string, size int64, buffer []byte) (storedBackupFile, error) {
	target, err := restorePath(directory, path)
	if err != nil {
		return storedBackupFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return storedBackupFile{}, err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return storedBackupFile{}, err
	}
	hash := sha256.New()
	written, copyErr := io.CopyBuffer(io.MultiWriter(file, hash), archive, buffer)
	closeErr := file.Close()
	if copyErr != nil || written != size {
		return storedBackupFile{}, errors.New("invalid backup archive")
	}
	if closeErr != nil {
		return storedBackupFile{}, closeErr
	}
	return storedBackupFile{path: target, size: written, sha256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func validateBackupArchive(manifest backupManifest, files map[string]storedBackupFile) error {
	if err := validateBackupManifest(manifest); err != nil {
		return err
	}
	seen, err := validateBackupFileEntries(manifest.Files, files)
	if err != nil {
		return err
	}
	if len(seen) != len(files) || !seen["instance.json"] || !seen["catalog.json"] {
		return errors.New("backup file set is incomplete")
	}
	return validateDurableBackupFiles(manifest.SourceInstanceID, manifest.DataVersion, files)
}

func validateBackupManifest(manifest backupManifest) error {
	if manifest.Schema != backupSchema || manifest.BackupVersion == "" || manifest.DataCompatibility == "" || manifest.SourceInstanceID == "" || !manifest.Complete {
		return errors.New("incomplete backup manifest")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		return errors.New("invalid backup creation time")
	}
	return nil
}

func validateBackupFileEntries(entries []backupFile, files map[string]storedBackupFile) (map[string]bool, error) {
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Path == "" || entry.Path == backupManifestPath || seen[entry.Path] || entry.Size < 0 {
			return nil, errors.New("invalid backup manifest files")
		}
		seen[entry.Path] = true
		file, ok := files[entry.Path]
		if !ok || file.size != entry.Size {
			return nil, errors.New("backup file set is incomplete")
		}
		if entry.SHA256 != file.sha256 {
			return nil, errors.New("backup file integrity check failed")
		}
	}
	return seen, nil
}

func validateDurableBackupFiles(sourceID, dataVersion string, files map[string]storedBackupFile) error {
	metadata, err := decodeBackupMetadataFile(files["instance.json"].path)
	if err != nil || metadata.Schema != "database.instance/v1" || metadata.InstanceID != sourceID || metadata.AdminAccount == "" || metadata.PasswordHash == "" {
		return errors.New("backup instance identity is invalid")
	}
	if dataVersion != "" && effectiveDataVersion(metadata) != dataVersion {
		return errors.New("backup data version is invalid")
	}
	if err := validateBackupCatalogFile(files["catalog.json"].path); err != nil {
		return errors.New("backup catalog is invalid")
	}
	return nil
}

func decodeBackupMetadataFile(path string) (instance.Metadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return instance.Metadata{}, err
	}
	defer file.Close()
	var metadata instance.Metadata
	if err := json.NewDecoder(file).Decode(&metadata); err != nil {
		return instance.Metadata{}, err
	}
	return metadata, nil
}

func decodeBackupMetadata(contents []byte) (instance.Metadata, error) {
	var metadata instance.Metadata
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return instance.Metadata{}, err
	}
	return metadata, nil
}

func validateBackupCatalog(contents []byte) error {
	var definition catalog.Definition
	if err := json.Unmarshal(contents, &definition); err != nil || definition.Namespaces == nil {
		return errors.New("invalid catalog")
	}
	return nil
}

func validateBackupCatalogFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var definition catalog.Definition
	if err := json.NewDecoder(file).Decode(&definition); err != nil || definition.Namespaces == nil {
		return errors.New("invalid catalog")
	}
	return nil
}

func restoreBackup(input, directory string) error {
	if err := validateRestoreRequest(input, directory); err != nil {
		return err
	}
	archive, err := loadBackupArchive(input)
	if err != nil {
		return err
	}
	defer archive.Close()
	metadataContents, err := os.ReadFile(archive.files["instance.json"].path)
	if err != nil {
		return err
	}
	metadataBytes, err := restoredMetadataBytes(metadataContents)
	if err != nil {
		return err
	}
	staging, existed, err := createRestoreStaging(directory)
	if err != nil {
		return err
	}
	if err := populateAndValidateRestore(staging, archive.files, metadataBytes); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := installRestoreStaging(staging, directory, existed); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	return nil
}

func validateRestoreRequest(input, directory string) error {
	if input == "" || directory == "" {
		return errors.New("restore requires --input and --data-dir")
	}
	return validateRestoreDestination(directory)
}

func restoredMetadataBytes(contents []byte) ([]byte, error) {
	metadata, err := restoredMetadata(contents)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func restoredMetadata(contents []byte) (instance.Metadata, error) {
	var source instance.Metadata
	if err := json.Unmarshal(contents, &source); err != nil {
		return instance.Metadata{}, errors.New("invalid source instance metadata")
	}
	return instance.NewRestoredMetadata(source)
}

func validateRestoreDestination(directory string) error {
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
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

func createRestoreStaging(directory string) (string, bool, error) {
	_, err := os.Stat(directory)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", false, err
	}
	staging, err := os.MkdirTemp(parent, ".database-restore-*")
	return staging, existed, err
}

func populateRestoreStaging(staging string, files map[string]storedBackupFile, metadata []byte) error {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		restoredPath, err := restorePath(staging, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(restoredPath), 0o700); err != nil {
			return err
		}
		if path == "instance.json" {
			if err := os.WriteFile(restoredPath, metadata, 0o600); err != nil {
				return err
			}
			continue
		}
		if err := copyStoredBackupFile(files[path].path, restoredPath); err != nil {
			return err
		}
	}
	return nil
}

func copyStoredBackupFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyBuffer(output, input, make([]byte, 64*1024))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func populateAndValidateRestore(staging string, files map[string]storedBackupFile, metadata []byte) error {
	if err := populateRestoreStaging(staging, files, metadata); err != nil {
		return err
	}
	if _, err := instance.Load(staging); err != nil {
		return fmt.Errorf("restored instance validation failed: %w", err)
	}
	if _, err := catalog.Open(staging); err != nil {
		return fmt.Errorf("restored catalog validation failed: %w", err)
	}
	return nil
}

func installRestoreStaging(staging, directory string, existed bool) error {
	if !existed {
		return os.Rename(staging, directory)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(staging, entry.Name()), filepath.Join(directory, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(staging)
}

func restorePath(directory, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." || filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return "", errors.New("unsafe backup path")
	}
	if directory == "" {
		return cleanName, nil
	}
	return filepath.Join(directory, cleanName), nil
}
