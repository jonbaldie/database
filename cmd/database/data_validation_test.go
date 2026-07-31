package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDataValidateReportsCompleteHealthyState(t *testing.T) {
	directory := t.TempDir()
	writeBackupFixture(t, filepath.Join(directory, "nested", "row.bin"), "row")
	writeInstanceFixture(t, directory)

	result := assertOperatorSuccess(t, []string{"data", "validate", "--data-directory", directory}, "data validate")
	if result["valid"] != true {
		t.Fatalf("validation result = %#v", result)
	}
	findings, ok := result["findings"].([]any)
	if !ok || len(findings) != 0 {
		t.Fatalf("validation findings = %#v", result["findings"])
	}
}

func TestDataValidateFailsClosedWithoutRepairingCorruption(t *testing.T) {
	directory := t.TempDir()
	writeInstanceFixture(t, directory)
	catalogPath := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"namespaces":{"broken":null}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	assertOperatorFailure(t, []string{"data", "validate", "--data-directory", directory}, "data validate", "invalid_artifact", 5)
	after, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("data validation repaired the damaged catalog")
	}
}

func TestDataInspectIsLimitedAndDoesNotRepairRecoveryArtifacts(t *testing.T) {
	directory := t.TempDir()
	writeInstanceFixture(t, directory)
	artifact := filepath.Join(directory, ".catalog-crash.tmp")
	if err := os.WriteFile(artifact, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := assertOperatorSuccess(t, []string{"data", "inspect", "--data-directory", directory}, "data inspect")
	if result["validated"] != false || result["integrity"] != "not-validated" || result["recovery_required"] != true {
		t.Fatalf("inspection result = %#v", result)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("inspection changed recovery artifact: %v", err)
	}
	if _, err := json.Marshal(result["entries"]); err != nil {
		t.Fatalf("inspection entries are not structured: %v", err)
	}
}
