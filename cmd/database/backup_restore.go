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
	manifest backupManifest
	files    map[string][]byte
}

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
	if len(args) == 0 {
		return errors.New("backup or restore requires an operation"), "invalid_input"
	}
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
	if err := rejectPathOverlap(directory, output); err != nil {
		return err
	}
	files, metadata, err := captureBackupSource(directory)
	if err != nil {
		return err
	}
	manifest := makeBackupManifest(metadata, files)
	return writeBackupArchive(output, manifest, files)
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

func captureBackupSource(directory string) (map[string][]byte, instance.Metadata, error) {
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
	if _, ok := files["instance.json"]; !ok {
		return nil, instance.Metadata{}, errors.New("source instance metadata is missing")
	}
	if _, ok := files["catalog.json"]; !ok {
		return nil, instance.Metadata{}, errors.New("source catalog is missing")
	}
	return files, metadata, nil
}

func readSourceFiles(directory string) (map[string][]byte, error) {
	collector := sourceFileCollector{directory: directory, files: make(map[string][]byte)}
	err := filepath.Walk(directory, collector.visit)
	return collector.files, err
}

type sourceFileCollector struct {
	directory string
	files     map[string][]byte
}

func (collector sourceFileCollector) visit(path string, info os.FileInfo, walkErr error) error {
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

func (collector sourceFileCollector) capture(relative, path string, info os.FileInfo) error {
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
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	collector.files[relative] = contents
	return nil
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
	info := buildinfo.Current()
	return backupManifest{Schema: backupSchema, BackupVersion: info.BackupCompatibility,
		DataCompatibility: info.DataCompatibility, DataVersion: effectiveDataVersion(metadata), SourceInstanceID: metadata.InstanceID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Complete: true, Files: entries}
}

func writeBackupArchive(output string, manifest backupManifest, files map[string][]byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(output), ".database-backup-*")
	if err != nil {
		return err
	}
	if err := writeBackupEntries(temporary, manifest, files); err != nil {
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
	_, err := loadBackupArchive(input)
	return err
}

func loadBackupArchive(input string) (backupArchive, error) {
	file, err := os.Open(input)
	if err != nil {
		return backupArchive{}, err
	}
	defer file.Close()
	entries, err := readBackupEntries(tar.NewReader(file))
	if err != nil {
		return backupArchive{}, err
	}
	manifest, files, err := splitBackupManifest(entries)
	if err != nil {
		return backupArchive{}, err
	}
	if err := validateBackupArchive(manifest, files); err != nil {
		return backupArchive{}, err
	}
	return backupArchive{manifest: manifest, files: files}, nil
}

type backupEntry struct {
	path      string
	contents  []byte
	manifest  bool
	directory bool
}

func readBackupEntries(archive *tar.Reader) ([]backupEntry, error) {
	entries := make([]backupEntry, 0)
	seen := make(map[string]bool)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, errors.New("invalid backup archive")
		}
		entry, err := readBackupEntry(archive, header, seen)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
}

func readBackupEntry(archive *tar.Reader, header *tar.Header, seen map[string]bool) (backupEntry, error) {
	path, err := restorePath("", header.Name)
	if err != nil || path == "" {
		return backupEntry{}, errors.New("unsafe backup path")
	}
	if header.FileInfo().IsDir() {
		return backupEntry{path: path, directory: true}, nil
	}
	if !header.FileInfo().Mode().IsRegular() {
		return backupEntry{}, errors.New("invalid backup archive entry")
	}
	if seen[path] {
		return backupEntry{}, errors.New("duplicate backup archive entry")
	}
	seen[path] = true
	contents, err := io.ReadAll(archive)
	if err != nil {
		return backupEntry{}, errors.New("invalid backup archive")
	}
	return backupEntry{path: path, contents: contents, manifest: path == backupManifestPath}, nil
}

func splitBackupManifest(entries []backupEntry) (backupManifest, map[string][]byte, error) {
	files := make(map[string][]byte)
	var manifest backupManifest
	manifestFound := false
	for _, entry := range entries {
		if entry.directory {
			continue
		}
		if entry.manifest {
			if manifestFound || json.Unmarshal(entry.contents, &manifest) != nil {
				return backupManifest{}, nil, errors.New("invalid backup manifest")
			}
			manifestFound = true
			continue
		}
		files[entry.path] = entry.contents
	}
	if !manifestFound {
		return backupManifest{}, nil, errors.New("backup manifest is missing")
	}
	return manifest, files, nil
}

func validateBackupArchive(manifest backupManifest, files map[string][]byte) error {
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

func validateBackupFileEntries(entries []backupFile, files map[string][]byte) (map[string]bool, error) {
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Path == "" || entry.Path == backupManifestPath || seen[entry.Path] || entry.Size < 0 {
			return nil, errors.New("invalid backup manifest files")
		}
		seen[entry.Path] = true
		contents, ok := files[entry.Path]
		if !ok || int64(len(contents)) != entry.Size {
			return nil, errors.New("backup file set is incomplete")
		}
		digest := sha256.Sum256(contents)
		if entry.SHA256 != hex.EncodeToString(digest[:]) {
			return nil, errors.New("backup file integrity check failed")
		}
	}
	return seen, nil
}

func validateDurableBackupFiles(sourceID, dataVersion string, files map[string][]byte) error {
	metadata, err := decodeBackupMetadata(files["instance.json"])
	if err != nil || metadata.Schema != "database.instance/v1" || metadata.InstanceID != sourceID || metadata.AdminAccount == "" || metadata.PasswordHash == "" {
		return errors.New("backup instance identity is invalid")
	}
	if dataVersion != "" && effectiveDataVersion(metadata) != dataVersion {
		return errors.New("backup data version is invalid")
	}
	if err := validateBackupCatalog(files["catalog.json"]); err != nil {
		return errors.New("backup catalog is invalid")
	}
	return nil
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

func restoreBackup(input, directory string) error {
	if err := validateRestoreRequest(input, directory); err != nil {
		return err
	}
	archive, err := loadBackupArchive(input)
	if err != nil {
		return err
	}
	metadataBytes, err := restoredMetadataBytes(archive.files["instance.json"])
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

func populateRestoreStaging(staging string, files map[string][]byte, metadata []byte) error {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		contents := files[path]
		if path == "instance.json" {
			contents = metadata
		}
		restoredPath, err := restorePath(staging, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(restoredPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(restoredPath, contents, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func populateAndValidateRestore(staging string, files map[string][]byte, metadata []byte) error {
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
