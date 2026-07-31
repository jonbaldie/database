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
	"sort"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/instance"
)

type dataRequest struct {
	directory string
}

type dataFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Summary  string `json:"summary"`
}

type dataValidationReport struct {
	Metadata  instance.Metadata
	Findings  []dataFinding
	Examined  []string
	Directory string
}

func dataCommand(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		return writeOperatorFailure(stdout, "data", newOperationID(), "invalid_input", 2, "data requires validate or inspect")
	}
	request, err := parseDataRequest(args[1:])
	if err != nil {
		return writeOperatorFailure(stdout, "data "+args[0], newOperationID(), "invalid_input", 2, err.Error())
	}
	switch args[0] {
	case "validate":
		return runDataValidation(request, stdout)
	case "inspect":
		return runDataInspection(request, stdout)
	default:
		return writeOperatorFailure(stdout, "data "+args[0], newOperationID(), "invalid_input", 2, fmt.Sprintf("unsupported data operation %q", args[0]))
	}
}

func parseDataRequest(args []string) (dataRequest, error) {
	options, err := operatorOptions(args, "--data-directory", "--data-dir")
	if err != nil {
		return dataRequest{}, err
	}
	directory := options["--data-directory"]
	if directory == "" {
		directory = options["--data-dir"]
	}
	if directory == "" {
		return dataRequest{}, errors.New("data operation requires --data-directory")
	}
	return dataRequest{directory: directory}, nil
}

func runDataValidation(request dataRequest, stdout io.Writer) int {
	report, err := validateDataDirectory(request.directory)
	if err != nil {
		return writeDataResult(stdout, "data validate", false, "precondition", err.Error(), nil)
	}
	details := dataValidationDetails(report)
	if len(report.Findings) != 0 {
		return writeDataResult(stdout, "data validate", false, "invalid_artifact", "durable data validation found damage", details)
	}
	return writeDataResult(stdout, "data validate", true, "success", "", details)
}

func runDataInspection(request dataRequest, stdout io.Writer) int {
	details, err := inspectDataDirectory(request.directory)
	if err != nil {
		return writeDataResult(stdout, "data inspect", false, "precondition", err.Error(), nil)
	}
	return writeDataResult(stdout, "data inspect", true, "success", "", details)
}

func writeDataResult(stdout io.Writer, operation string, success bool, exitClass, diagnostic string, details map[string]any) int {
	result := operatorResult(operation, newOperationID(), success, exitClass, diagnostic)
	for key, value := range details {
		result[key] = value
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return 1
	}
	if success {
		return 0
	}
	return operatorExitCode(exitClass)
}

func validateDataDirectory(directory string) (dataValidationReport, error) {
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return dataValidationReport{}, errors.New("data directory does not exist")
	}
	if err != nil {
		return dataValidationReport{}, fmt.Errorf("inspect data directory: %w", err)
	}
	if !info.IsDir() {
		return dataValidationReport{}, errors.New("data directory is not a directory")
	}
	report := dataValidationReport{Directory: directory, Findings: []dataFinding{}, Examined: []string{}}
	collectDataEntries(directory, &report)
	validateDataMetadata(directory, &report)
	validateDataCatalog(directory, &report)
	sort.Strings(report.Examined)
	return report, nil
}

func collectDataEntries(directory string, report *dataValidationReport) {
	_ = filepath.Walk(directory, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			report.Findings = append(report.Findings, dataFinding{Code: "unreadable_entry", Severity: "error", Path: relativeDataPath(directory, path), Summary: "durable entry cannot be read"})
			return nil
		}
		if path == directory {
			return nil
		}
		relative := relativeDataPath(directory, path)
		if runtimeDataPath(relative) {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			report.Findings = append(report.Findings, dataFinding{Code: "unsupported_entry", Severity: "error", Path: relative, Summary: "durable entry is not a regular file"})
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			report.Findings = append(report.Findings, dataFinding{Code: "unreadable_entry", Severity: "error", Path: relative, Summary: "durable entry cannot be read"})
			return nil
		}
		digest := sha256.Sum256(contents)
		report.Examined = append(report.Examined, relative+"#"+hex.EncodeToString(digest[:]))
		return nil
	})
}

func relativeDataPath(directory, path string) string {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func runtimeDataPath(path string) bool {
	base := filepath.Base(path)
	if base == ".running.lock" || base == ".database-state" || strings.HasPrefix(base, ".database-state-") {
		return true
	}
	return false
}

func validateDataMetadata(directory string, report *dataValidationReport) {
	metadata, err := instance.Load(directory)
	if err != nil {
		report.Findings = append(report.Findings, dataFinding{Code: "instance_metadata_invalid", Severity: "error", Path: "instance.json", Summary: "instance metadata is missing or invalid"})
		return
	}
	report.Metadata = metadata
	if metadata.State != "stopped" {
		report.Findings = append(report.Findings, dataFinding{Code: "instance_state_invalid", Severity: "error", Path: "instance.json", Summary: "instance is not in a stopped state"})
	}
	if _, err := os.Stat(filepath.Join(directory, instance.UpgradeIncompleteMarker)); err == nil {
		report.Findings = append(report.Findings, dataFinding{Code: "upgrade_incomplete", Severity: "error", Path: instance.UpgradeIncompleteMarker, Summary: "upgrade is incomplete and requires an explicit resume"})
	}
}

func validateDataCatalog(directory string, report *dataValidationReport) {
	path := filepath.Join(directory, "catalog.json")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		report.Findings = append(report.Findings, dataFinding{Code: "catalog_missing", Severity: "error", Path: "catalog.json", Summary: "catalog is missing or not a regular file"})
		return
	}
	if _, err := catalog.Open(directory); err != nil {
		report.Findings = append(report.Findings, dataFinding{Code: "catalog_invalid", Severity: "error", Path: "catalog.json", Summary: "catalog cannot be validated"})
	}
	if incompleteCatalogCommit(directory) {
		report.Findings = append(report.Findings, dataFinding{Code: "catalog_recovery_artifact", Severity: "error", Path: ".catalog-*.tmp", Summary: "an incomplete catalog commit is present"})
	}
	if incompleteInitialization(directory) {
		report.Findings = append(report.Findings, dataFinding{Code: "initialization_incomplete", Severity: "error", Path: instanceInitializationMarker, Summary: "initialization is incomplete"})
	}
}

const instanceInitializationMarker = ".database-initializing"

func incompleteCatalogCommit(directory string) bool {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".catalog-") && strings.HasSuffix(entry.Name(), ".tmp") {
			return true
		}
	}
	return false
}

func incompleteInitialization(directory string) bool {
	_, err := os.Stat(filepath.Join(directory, instanceInitializationMarker))
	return err == nil
}

func dataValidationDetails(report dataValidationReport) map[string]any {
	details := map[string]any{"data_directory": report.Directory, "valid": len(report.Findings) == 0, "findings": report.Findings, "examined": report.Examined}
	if report.Metadata.InstanceID != "" {
		details["instance_id"] = report.Metadata.InstanceID
		details["data_version"] = effectiveDataVersion(report.Metadata)
	}
	return details
}

func inspectDataDirectory(directory string) (map[string]any, error) {
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("data directory does not exist")
	}
	if err != nil || !info.IsDir() {
		return nil, errors.New("data directory is not a directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect data directory: %w", err)
	}
	metadata, metadataErr := instance.Load(directory)
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Name())
	}
	sort.Strings(paths)
	details := map[string]any{"data_directory": directory, "validated": false, "examined": []string{"directory", "instance.json", "catalog.json"}, "entries": paths, "recovery_required": incompleteCatalogCommit(directory) || incompleteInitialization(directory)}
	if metadataErr != nil {
		details["integrity"] = "not-validated"
		details["inspection_finding"] = "instance metadata is unavailable"
		return details, nil
	}
	details["instance_id"] = metadata.InstanceID
	details["data_version"] = effectiveDataVersion(metadata)
	details["state"] = metadata.State
	details["upgrade_required"] = metadata.State == "upgrade-incomplete"
	details["integrity"] = "not-validated"
	return details, nil
}
