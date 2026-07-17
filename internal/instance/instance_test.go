package instance

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestInitializeAllowsExactlyOneConcurrentCreator(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	start := make(chan struct{})
	results := make(chan initializationResult, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			metadata, err := Initialize(directory, "admin", "secret")
			results <- initializationResult{metadata: metadata, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(results)

	created, rejected := splitInitializationResults(results)
	if created.InstanceID == "" || rejected == nil {
		t.Fatalf("concurrent initialization results = created:%#v rejected:%v", created, rejected)
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatalf("load created instance: %v", err)
	}
	if loaded.InstanceID != created.InstanceID {
		t.Fatalf("stored instance identity = %q, want %q", loaded.InstanceID, created.InstanceID)
	}
	if _, err := os.Stat(filepath.Join(directory, initializationLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initialization claim remains after successful initialization: %v", err)
	}
}

func TestDiscardedInitializationClaimLeavesNoArtifacts(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	claim, err := claimInitialization(directory)
	if err != nil {
		t.Fatalf("claim initialization: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claim.staging, "catalog.json"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("create staged artifact: %v", err)
	}
	if err := claim.discard(); err != nil {
		t.Fatalf("discard initialization: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read discarded directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("discarded initialization artifacts = %#v", entries)
	}
}

func TestFailedInstallRemovesPartialCatalogAndClaim(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	claim, err := claimInitialization(directory)
	if err != nil {
		t.Fatalf("claim initialization: %v", err)
	}
	paths := initializationPaths{directory: claim.directory, staging: claim.staging}
	if err := paths.writeStaged([]byte("metadata")); err != nil {
		t.Fatalf("stage instance files: %v", err)
	}
	if err := os.Remove(paths.metadataTemporary()); err != nil {
		t.Fatalf("make metadata installation fail: %v", err)
	}
	if err := paths.commit(); err == nil {
		t.Fatal("commit succeeded without staged metadata")
	}
	if err := claim.discard(); err != nil {
		t.Fatalf("discard failed initialization: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read failed initialization directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed initialization artifacts = %#v", entries)
	}
}

type initializationResult struct {
	metadata Metadata
	err      error
}

func splitInitializationResults(results <-chan initializationResult) (Metadata, error) {
	var created Metadata
	var rejected error
	for result := range results {
		if result.err == nil {
			if created.InstanceID != "" {
				return Metadata{}, nil
			}
			created = result.metadata
			continue
		}
		if rejected != nil {
			return Metadata{}, nil
		}
		rejected = result.err
	}
	return created, rejected
}
