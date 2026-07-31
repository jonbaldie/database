package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jonbaldie/database/internal/buildinfo"
	"github.com/jonbaldie/database/internal/instance"
)

const upgradeMarkerSchema = "database.upgrade/v1"

type upgradeRequest struct {
	directory string
	backup    string
	target    string
	yes       bool
	rolling   bool
	downgrade bool
}

type upgradeMarker struct {
	Schema           string `json:"schema"`
	TargetVersion    string `json:"target_version"`
	SourceInstanceID string `json:"source_instance_id"`
	StartedAt        string `json:"started_at"`
}

type dataVersion struct {
	major int
	minor int
	patch int
}

var upgradeAvailableSpace = availableUpgradeSpace

func upgradeCommand(args []string, stdout, stderr io.Writer) int {
	output, filtered, err := parseCommandOutput(args, true)
	if err != nil {
		return newOperationReporter("upgrade", commandOutput{result: "json", progress: "none"}, stdout, stderr).failure("invalid_input", "", err.Error(), nil)
	}
	if !containsOutputControl(args) {
		output.result = "json"
		output.legacy = true
	}
	reporter := newOperationReporter("upgrade", output, stdout, stderr)
	reporter.progress("preflight")
	request, err := parseUpgradeRequest(filtered)
	if err != nil {
		return reporter.failure("invalid_input", "", err.Error(), nil)
	}
	reporter.progress("upgrading")
	err, exitClass := executeUpgrade(request)
	if err != nil {
		return reporter.failure(exitClass, "", err.Error(), nil)
	}
	reporter.progress("validating")
	return reporter.success(nil)
}

func parseUpgradeRequest(args []string) (upgradeRequest, error) {
	request := upgradeRequest{}
	argumentCount := len(args)
	for index := 0; index < argumentCount; index++ {
		nextIndex, err := consumeUpgradeArgument(args, index, &request)
		if err != nil {
			return upgradeRequest{}, err
		}
		index = nextIndex
	}
	return validateUpgradeRequest(request)
}

func consumeUpgradeArgument(args []string, index int, request *upgradeRequest) (int, error) {
	name, value, hasValue := strings.Cut(args[index], "=")
	if isUpgradeBoolean(name) {
		return index, setUpgradeBoolean(request, name, hasValue)
	}
	if !isUpgradeValueFlag(name) {
		return index, fmt.Errorf("unknown flag %q", name)
	}
	value, nextIndex, err := upgradeFlagValue(args, index, name, value, hasValue)
	if err != nil {
		return index, err
	}
	return nextIndex, setUpgradeValue(request, name, value)
}

func isUpgradeBoolean(name string) bool {
	return name == "--yes" || name == "--rolling" || name == "--downgrade"
}

func isUpgradeValueFlag(name string) bool {
	switch name {
	case "--data-directory", "--data-dir", "--backup", "--input", "--target-version":
		return true
	default:
		return false
	}
}

func setUpgradeBoolean(request *upgradeRequest, name string, hasValue bool) error {
	if hasValue {
		return fmt.Errorf("%s does not take a value", name)
	}
	switch name {
	case "--yes":
		if request.yes {
			return errors.New("--yes may be specified once")
		}
		request.yes = true
	case "--rolling":
		request.rolling = true
	case "--downgrade":
		request.downgrade = true
	}
	return nil
}

func setUpgradeValue(request *upgradeRequest, name, value string) error {
	switch name {
	case "--data-directory", "--data-dir":
		if request.directory != "" {
			return errors.New("data directory may be specified once")
		}
		request.directory = value
	case "--backup", "--input":
		if request.backup != "" {
			return errors.New("backup may be specified once")
		}
		request.backup = value
	case "--target-version":
		if request.target != "" {
			return errors.New("target version may be specified once")
		}
		request.target = value
	}
	return nil
}

func validateUpgradeRequest(request upgradeRequest) (upgradeRequest, error) {
	if request.directory == "" || request.backup == "" {
		return upgradeRequest{}, errors.New("upgrade requires --data-directory, --backup, and --yes")
	}
	if !request.yes {
		return upgradeRequest{}, errors.New("upgrade requires --yes for non-interactive use")
	}
	if request.target == "" {
		request.target = defaultUpgradeTarget()
	}
	return request, nil
}

func upgradeFlagValue(args []string, index int, name, value string, hasValue bool) (string, int, error) {
	if hasValue {
		if value == "" {
			return "", index, fmt.Errorf("%s requires a non-empty value", name)
		}
		return value, index, nil
	}
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
		return "", index, fmt.Errorf("%s requires a non-empty value", name)
	}
	return args[index+1], index + 1, nil
}

func defaultUpgradeTarget() string {
	target := strings.SplitN(buildinfo.ProductVersion, "-", 2)[0]
	if _, err := parseDataVersion(target); err == nil {
		return target
	}
	return instance.CurrentDataVersion
}

func executeUpgrade(request upgradeRequest) (error, string) {
	target, err := parseDataVersion(request.target)
	if err != nil {
		return err, "invalid_input"
	}
	if err := rejectUnsupportedUpgrade(request); err != nil {
		return err, "precondition"
	}
	metadata, marker, lock, exitClass, err := prepareUpgrade(request, target)
	if err != nil {
		return err, exitClass
	}
	defer releaseUpgradeLock(lock)
	return completeUpgrade(request, metadata, marker)
}

func rejectUnsupportedUpgrade(request upgradeRequest) error {
	if request.rolling {
		return errors.New("rolling upgrades are unsupported; stop the server first")
	}
	if request.downgrade {
		return errors.New("downgrades are unsupported")
	}
	return nil
}

func prepareUpgrade(request upgradeRequest, target dataVersion) (instance.Metadata, *upgradeMarker, *os.File, string, error) {
	metadata, marker, err := preflightUpgrade(request, target)
	if err != nil {
		return instance.Metadata{}, nil, nil, "precondition", err
	}
	lock, err := claimUpgradeDirectory(request.directory)
	if err != nil {
		return instance.Metadata{}, nil, nil, "precondition", err
	}
	metadata, marker, err = preflightUpgrade(request, target)
	if err != nil {
		releaseUpgradeLock(lock)
		return instance.Metadata{}, nil, nil, "precondition", err
	}
	if marker == nil {
		marker = &upgradeMarker{Schema: upgradeMarkerSchema, TargetVersion: request.target, SourceInstanceID: metadata.InstanceID, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := writeUpgradeMarker(request.directory, *marker); err != nil {
			releaseUpgradeLock(lock)
			return instance.Metadata{}, nil, nil, "operation_failed", err
		}
	}
	if err := markUpgradeIncomplete(request.directory, metadata); err != nil {
		return instance.Metadata{}, nil, lock, "operation_failed", err
	}
	return metadata, marker, lock, "success", nil
}

func completeUpgrade(request upgradeRequest, metadata instance.Metadata, marker *upgradeMarker) (error, string) {
	metadata.DataVersion = request.target
	metadata.State = "stopped"
	if err := writeInstanceMetadata(request.directory, metadata); err != nil {
		return err, "operation_failed"
	}
	if marker == nil {
		return errors.New("upgrade marker is missing"), "operation_failed"
	}
	if err := os.Remove(filepath.Join(request.directory, instance.UpgradeIncompleteMarker)); err != nil {
		return err, "operation_failed"
	}
	return nil, "success"
}

func preflightUpgrade(request upgradeRequest, target dataVersion) (instance.Metadata, *upgradeMarker, error) {
	metadata, marker, err := loadUpgradeState(request)
	if err != nil {
		return instance.Metadata{}, nil, err
	}
	if err := validateUpgradeTarget(metadata, marker, request.target, target); err != nil {
		return instance.Metadata{}, nil, err
	}
	archive, err := loadBackupArchive(request.backup)
	if err != nil {
		return instance.Metadata{}, nil, fmt.Errorf("invalid matching backup: %w", err)
	}
	if err := validateUpgradeBackup(request, archive, metadata, marker != nil); err != nil {
		return instance.Metadata{}, nil, err
	}
	if err := validateUpgradeSpace(request.directory, archive); err != nil {
		return instance.Metadata{}, nil, err
	}
	return metadata, marker, nil
}

func loadUpgradeState(request upgradeRequest) (instance.Metadata, *upgradeMarker, error) {
	info, err := os.Stat(request.directory)
	if err != nil || !info.IsDir() {
		return instance.Metadata{}, nil, errors.New("data directory does not exist")
	}
	metadata, err := instance.Load(request.directory)
	if err != nil {
		return instance.Metadata{}, nil, fmt.Errorf("invalid data directory: %w", err)
	}
	marker, err := readUpgradeMarker(request.directory)
	if err != nil {
		return instance.Metadata{}, nil, err
	}
	if err := validateUpgradeState(metadata, marker, request); err != nil {
		return instance.Metadata{}, nil, err
	}
	return metadata, marker, nil
}

func validateUpgradeState(metadata instance.Metadata, marker *upgradeMarker, request upgradeRequest) error {
	resuming := marker != nil
	if metadata.State != "stopped" && !(resuming && metadata.State == "upgrade-incomplete") {
		return errors.New("data directory must be stopped")
	}
	if marker != nil && (marker.TargetVersion != request.target || marker.SourceInstanceID != metadata.InstanceID) {
		return errors.New("upgrade-incomplete directory can resume only the same target")
	}
	return nil
}

func validateUpgradeTarget(metadata instance.Metadata, marker *upgradeMarker, requested string, target dataVersion) error {
	if target.major != 0 || target.minor != 1 {
		return errors.New("target version is outside the supported 0.1.x release line")
	}
	current, err := parseDataVersion(effectiveDataVersion(metadata))
	if err != nil {
		return errors.New("data directory has an unsupported data version")
	}
	comparison := compareDataVersion(target, current)
	if comparison < 0 {
		return errors.New("downgrades are unsupported")
	}
	if comparison == 0 && marker == nil {
		return errors.New("data directory is already at the target version")
	}
	if marker != nil && marker.TargetVersion != requested {
		return errors.New("upgrade-incomplete directory can resume only the same target")
	}
	return nil
}

func validateUpgradeBackup(request upgradeRequest, archive backupArchive, metadata instance.Metadata, resuming bool) error {
	buildInfo := buildinfo.Current()
	if archive.manifest.DataCompatibility != buildInfo.DataCompatibility || archive.manifest.BackupVersion != buildInfo.BackupCompatibility || archive.manifest.SourceInstanceID != metadata.InstanceID {
		return errors.New("backup is not compatible with this data directory")
	}
	if err := backupMatchesSource(request.directory, archive, metadata, resuming); err != nil {
		return err
	}
	return nil
}

func validateUpgradeSpace(directory string, archive backupArchive) error {
	free, err := upgradeAvailableSpace(directory)
	if err != nil {
		return fmt.Errorf("check free space: %w", err)
	}
	if free < requiredUpgradeSpace(archive) {
		return errors.New("data directory does not have enough free space for the upgrade")
	}
	return nil
}

func parseDataVersion(value string) (dataVersion, error) {
	parts := strings.Split(strings.SplitN(value, "-", 2)[0], ".")
	if len(parts) != 3 {
		return dataVersion{}, errors.New("version must be major.minor.patch")
	}
	numbers := [3]int{}
	for index, part := range parts {
		if part == "" {
			return dataVersion{}, errors.New("version must be major.minor.patch")
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return dataVersion{}, errors.New("version must be major.minor.patch")
		}
		numbers[index] = number
	}
	return dataVersion{major: numbers[0], minor: numbers[1], patch: numbers[2]}, nil
}

func compareDataVersion(left, right dataVersion) int {
	if left.major != right.major {
		return compareInts(left.major, right.major)
	}
	if left.minor != right.minor {
		return compareInts(left.minor, right.minor)
	}
	return compareInts(left.patch, right.patch)
}

func compareInts(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func backupMatchesSource(directory string, archive backupArchive, metadata instance.Metadata, resuming bool) error {
	var backupMetadata instance.Metadata
	if err := json.Unmarshal(archive.files["instance.json"], &backupMetadata); err != nil {
		return errors.New("backup instance metadata is invalid")
	}
	if err := validateBackupMetadata(backupMetadata, metadata, resuming); err != nil {
		return err
	}
	return compareBackupFiles(directory, archive.files)
}

func validateBackupMetadata(backupMetadata, current instance.Metadata, resuming bool) error {
	if backupMetadata.InstanceID != current.InstanceID || backupMetadata.AdminAccount != current.AdminAccount || backupMetadata.PasswordHash != current.PasswordHash {
		return errors.New("backup does not match the current instance")
	}
	if !resuming && effectiveDataVersion(backupMetadata) != effectiveDataVersion(current) {
		return errors.New("backup does not match the current data version")
	}
	return nil
}

func compareBackupFiles(directory string, files map[string][]byte) error {
	currentFiles, err := readSourceFiles(directory)
	if err != nil || len(currentFiles) != len(files) {
		return errors.New("backup does not match the current data directory")
	}
	for path, contents := range files {
		if path == "instance.json" {
			continue
		}
		current, ok := currentFiles[path]
		if !ok || !equalBytes(current, contents) {
			return errors.New("backup does not match the current data directory")
		}
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	leftHash := sha256.Sum256(left)
	rightHash := sha256.Sum256(right)
	return hex.EncodeToString(leftHash[:]) == hex.EncodeToString(rightHash[:])
}

func requiredUpgradeSpace(archive backupArchive) uint64 {
	var total uint64
	for _, contents := range archive.files {
		total += uint64(len(contents))
	}
	return total + 64*1024
}

func availableUpgradeSpace(directory string) (uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(directory, &stats); err != nil {
		return 0, err
	}
	return uint64(stats.Bavail) * uint64(stats.Bsize), nil
}

func claimUpgradeDirectory(directory string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join(directory, ".running.lock"), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("claim data directory: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, errors.New("data directory is already in use")
	}
	return lock, nil
}

func releaseUpgradeLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

func readUpgradeMarker(directory string) (*upgradeMarker, error) {
	contents, err := os.ReadFile(filepath.Join(directory, instance.UpgradeIncompleteMarker))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marker upgradeMarker
	if err := json.Unmarshal(contents, &marker); err != nil || marker.Schema != upgradeMarkerSchema || marker.TargetVersion == "" || marker.SourceInstanceID == "" {
		return nil, errors.New("invalid upgrade-incomplete marker")
	}
	if _, err := parseDataVersion(marker.TargetVersion); err != nil {
		return nil, errors.New("invalid upgrade-incomplete target")
	}
	return &marker, nil
}

func writeUpgradeMarker(directory string, marker upgradeMarker) error {
	contents, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return writeUpgradeFile(filepath.Join(directory, instance.UpgradeIncompleteMarker), contents)
}

func markUpgradeIncomplete(directory string, metadata instance.Metadata) error {
	metadata.State = "upgrade-incomplete"
	return writeInstanceMetadata(directory, metadata)
}

func writeInstanceMetadata(directory string, metadata instance.Metadata) error {
	contents, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return writeUpgradeFile(filepath.Join(directory, "instance.json"), contents)
}

func writeUpgradeFile(path string, contents []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".database-upgrade-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(contents)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
