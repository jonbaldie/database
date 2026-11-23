package instance

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/credential"
)

func TestInitializeCreatesInitialAdministratorInCatalog(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	if _, err := Initialize(directory, "admin", "contract-valid-password"); err != nil {
		t.Fatalf("initialize instance: %v", err)
	}
	store, err := catalog.Open(directory)
	if err != nil {
		t.Fatalf("open initialized catalog: %v", err)
	}
	admin, found := store.Account("admin")
	wantGrants := []catalog.Grant{
		{Privilege: "ACCOUNT_MANAGER"},
		{Privilege: "NAMESPACE_MANAGER"},
		{Privilege: "OPERATIONAL_OBSERVATION"},
		{Privilege: "OPERATIONAL_CONTROL"},
	}
	if !found || admin.PasswordHash != credential.PasswordHash("contract-valid-password") || !reflect.DeepEqual(admin.Grants, wantGrants) {
		t.Fatalf("initial administrator = %#v, found=%t", admin, found)
	}
	metadata, err := os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), `"password_hash"`) {
		t.Fatalf("instance metadata contains a second password verifier: %s", metadata)
	}
}

func TestLoadAndClearLegacyPasswordHash(t *testing.T) {
	directory := t.TempDir()
	contents := []byte(`{"schema":"database.instance/v1","instance_id":"legacy","state":"stopped","admin_account":"admin","password_hash":"legacy-hash"}`)
	if err := os.WriteFile(filepath.Join(directory, "instance.json"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := Load(directory)
	if err != nil {
		t.Fatalf("load legacy metadata: %v", err)
	}
	if metadata.LegacyPasswordHash != "legacy-hash" {
		t.Fatalf("legacy password hash = %q", metadata.LegacyPasswordHash)
	}
	if err := ClearLegacyPasswordHash(directory, &metadata); err != nil {
		t.Fatalf("clear legacy password hash: %v", err)
	}
	updated, err := os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated), `"password_hash"`) || metadata.LegacyPasswordHash != "" {
		t.Fatalf("legacy hash was not removed: metadata=%#v contents=%s", metadata, updated)
	}
}

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
			metadata, err := Initialize(directory, "admin", "contract-valid-password")
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
	if err := paths.writeStaged([]byte("catalog"), []byte("metadata")); err != nil {
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

func TestInstallDoesNotOverwriteConcurrentCatalog(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	claim, err := claimInitialization(directory)
	if err != nil {
		t.Fatalf("claim initialization: %v", err)
	}
	paths := initializationPaths{directory: claim.directory, staging: claim.staging}
	if err := paths.writeStaged([]byte("catalog"), []byte("metadata")); err != nil {
		t.Fatalf("stage instance files: %v", err)
	}
	if err := os.WriteFile(paths.catalog(), []byte("existing"), 0o600); err != nil {
		t.Fatalf("create concurrent catalog: %v", err)
	}
	if err := paths.commit(); err == nil {
		t.Fatal("commit overwrote an existing catalog")
	}
	contents, err := os.ReadFile(paths.catalog())
	if err != nil {
		t.Fatalf("read concurrent catalog: %v", err)
	}
	if string(contents) != "existing" {
		t.Fatalf("concurrent catalog = %q", contents)
	}
	if err := claim.discard(); err != nil {
		t.Fatalf("discard failed initialization: %v", err)
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

func TestInitializeRejectsCredentialsOutsideTheContract(t *testing.T) {
	cases := []struct {
		account, password string
		want              error
	}{
		{"admin", "secret", credential.ErrInvalidPassword},
		{"admin@localhost", "contract-valid-password", credential.ErrInvalidAccountName},
	}
	for _, test := range cases {
		directory := filepath.Join(t.TempDir(), "instance")
		if _, err := Initialize(directory, test.account, test.password); !errors.Is(err, test.want) {
			t.Fatalf("Initialize(%q) error = %v, want %v", test.account, err, test.want)
		}
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Initialize created the data directory after rejecting credentials: %v", err)
		}
	}
}

func TestReadPasswordAppliesPolicyAfterLineEndingIsRemoved(t *testing.T) {
	password, err := ReadPassword("", strings.NewReader("contract-valid-password\r\n"))
	if err != nil || password != "contract-valid-password" {
		t.Fatalf("ReadPassword = %q, %v", password, err)
	}
	for _, input := range []string{"", "\n", "eleven-byte\n", "eleven-byte\r\n", strings.Repeat("p", 1025), "valid-prefix\xff\n"} {
		if _, err := ReadPassword("", strings.NewReader(input)); !errors.Is(err, credential.ErrInvalidPassword) {
			t.Fatalf("ReadPassword(%d bytes) error = %v, want ErrInvalidPassword", len(input), err)
		}
	}
}
