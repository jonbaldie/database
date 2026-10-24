// Package instance owns the durable identity created by database init.
package instance

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
	"github.com/jonbaldie/database/internal/credential"
)

type Metadata struct {
	Schema             string `json:"schema"`
	InstanceID         string `json:"instance_id"`
	SourceInstanceID   string `json:"source_instance_id,omitempty"`
	DataVersion        string `json:"data_version,omitempty"`
	State              string `json:"state"`
	AdminAccount       string `json:"admin_account"`
	LegacyPasswordHash string `json:"-"`
}

func (metadata *Metadata) UnmarshalJSON(contents []byte) error {
	type metadataFields Metadata
	var stored metadataFields
	if err := json.Unmarshal(contents, &stored); err != nil {
		return err
	}
	var legacy struct {
		PasswordHash string `json:"password_hash"`
	}
	if err := json.Unmarshal(contents, &legacy); err != nil {
		return err
	}
	*metadata = Metadata(stored)
	metadata.LegacyPasswordHash = legacy.PasswordHash
	return nil
}

const (
	CurrentDataVersion      = "0.1.0"
	UpgradeIncompleteMarker = ".database-upgrade-incomplete"
)

func Load(directory string) (Metadata, error) {
	contents, err := os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		return Metadata{}, err
	}
	var metadata Metadata
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("decode instance metadata: %w", err)
	}
	if metadata.Schema != "database.instance/v1" || metadata.InstanceID == "" || metadata.AdminAccount == "" {
		return Metadata{}, errors.New("invalid instance metadata")
	}
	return metadata, nil
}

// ClearLegacyPasswordHash removes the old init credential from instance.json
// after the catalog has imported it or has shown that it is no longer needed.
func ClearLegacyPasswordHash(directory string, metadata *Metadata) error {
	if metadata.LegacyPasswordHash == "" {
		return nil
	}
	contents, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instance metadata: %w", err)
	}
	contents = append(contents, '\n')
	if err := replaceInstanceMetadata(directory, contents); err != nil {
		return err
	}
	metadata.LegacyPasswordHash = ""
	return nil
}

func replaceInstanceMetadata(directory string, contents []byte) error {
	file, err := os.CreateTemp(directory, ".instance-*.tmp")
	if err != nil {
		return fmt.Errorf("stage instance metadata: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := writeSyncedFile(file, contents); err != nil {
		return fmt.Errorf("write instance metadata: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(directory, "instance.json")); err != nil {
		return fmt.Errorf("install instance metadata: %w", err)
	}
	return syncInstanceDirectory(directory)
}

func writeSyncedFile(file *os.File, contents []byte) error {
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncInstanceDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open data directory: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync data directory: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close data directory: %w", err)
	}
	return nil
}

// NewRestoredMetadata gives a restored instance a new identity and keeps the
// source identity and administrator name as provenance. It leaves accounts in
// the catalog and sets the restored instance state to stopped.
func NewRestoredMetadata(source Metadata) (Metadata, error) {
	if source.Schema != "database.instance/v1" || source.InstanceID == "" || source.AdminAccount == "" {
		return Metadata{}, errors.New("invalid source instance metadata")
	}
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return Metadata{}, fmt.Errorf("generate restored instance identity: %w", err)
	}
	result := source
	result.InstanceID = hex.EncodeToString(idBytes[:])
	result.SourceInstanceID = source.InstanceID
	if result.DataVersion == "" {
		result.DataVersion = CurrentDataVersion
	}
	result.State = "stopped"
	return result, nil
}

// Initialize creates one new, stopped instance in an empty directory.
func Initialize(directory, account, password string) (Metadata, error) {
	if err := validateInitializationInput(directory, account, password); err != nil {
		return Metadata{}, err
	}
	claim, err := claimInitialization(directory)
	if err != nil {
		return Metadata{}, err
	}
	metadata, err := newMetadata(account)
	if err != nil {
		return Metadata{}, joinInitializationErrors(err, claim.discard())
	}
	initialAccount := catalog.InitialAdministrator(account, credential.PasswordHash(password))
	if err := persistInitializedInstance(claim, metadata, initialAccount); err != nil {
		return Metadata{}, joinInitializationErrors(err, claim.discard())
	}
	if err := claim.release(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func validateInitializationInput(directory, account, password string) error {
	if directory == "" {
		return errors.New("data directory is required")
	}
	return ValidateCredentials(account, password)
}

// ValidateCredentials applies the account-administration credential policy to
// the initial account, so init never creates an account that CREATE USER
// would reject. Failures wrap credential.ErrInvalidAccountName or
// credential.ErrInvalidPassword.
func ValidateCredentials(account, password string) error {
	if err := credential.ValidateAccountName(account); err != nil {
		return fmt.Errorf("initial account: %w", err)
	}
	if err := credential.ValidatePassword(password); err != nil {
		return fmt.Errorf("initial password: %w", err)
	}
	return nil
}

const initializationLockName = ".database-initializing"

type initializationClaim struct {
	directory string
	staging   string
}

func claimInitialization(directory string) (initializationClaim, error) {
	if err := prepareDirectory(directory); err != nil {
		return initializationClaim{}, err
	}
	staging := filepath.Join(directory, initializationLockName)
	if err := os.Mkdir(staging, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return initializationClaim{}, errors.New("data directory initialization is already in progress")
		}
		return initializationClaim{}, fmt.Errorf("claim data directory initialization: %w", err)
	}
	claim := initializationClaim{directory: directory, staging: staging}
	if err := claim.validateExclusiveTarget(); err != nil {
		return initializationClaim{}, joinInitializationErrors(err, claim.discard())
	}
	return claim, nil
}

func prepareDirectory(directory string) error {
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}
		return ValidateInitializationTarget(directory)
	}
	return validateInitializationTarget(directory, info, err)
}

// ValidateInitializationTarget reports whether directory remains eligible for
// explicit initialization without creating or changing it.
func ValidateInitializationTarget(directory string) error {
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return validateInitializationTarget(directory, info, err)
}

func validateInitializationTarget(directory string, info os.FileInfo, err error) error {
	if err != nil {
		return fmt.Errorf("inspect data directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("data directory is not a directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("inspect data directory: %w", err)
	}
	if hasOnlyInitializationLock(entries) {
		return errors.New("data directory initialization is already in progress")
	}
	if len(entries) != 0 {
		return errors.New("data directory is not empty")
	}
	return nil
}

func hasOnlyInitializationLock(entries []os.DirEntry) bool {
	return len(entries) == 1 && entries[0].Name() == initializationLockName && entries[0].IsDir()
}

func (claim initializationClaim) release() error {
	if err := os.Remove(claim.staging); err != nil {
		return fmt.Errorf("finish data directory initialization: %w", err)
	}
	return nil
}

func (claim initializationClaim) discard() error {
	paths := initializationPaths{directory: claim.directory, staging: claim.staging}
	return joinInitializationErrors(paths.removeTemporary(), removeDirectory(claim.staging))
}

func (claim initializationClaim) validateExclusiveTarget() error {
	entries, err := os.ReadDir(claim.directory)
	if err != nil {
		return fmt.Errorf("inspect data directory: %w", err)
	}
	if hasOnlyInitializationLock(entries) {
		return nil
	}
	return errors.New("data directory is not empty")
}

func joinInitializationErrors(primary, cleanup error) error { return errors.Join(primary, cleanup) }

func removeDirectory(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("discard data directory initialization: %w", err)
	}
	return nil
}

func newMetadata(account string) (Metadata, error) {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return Metadata{}, fmt.Errorf("generate instance identity: %w", err)
	}
	return Metadata{
		Schema:       "database.instance/v1",
		InstanceID:   hex.EncodeToString(idBytes[:]),
		DataVersion:  CurrentDataVersion,
		State:        "stopped",
		AdminAccount: account,
	}, nil
}

func persistInitializedInstance(claim initializationClaim, metadata Metadata, initialAccount catalog.Account) error {
	metadataContents, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instance metadata: %w", err)
	}
	metadataContents = append(metadataContents, '\n')
	var catalogContents bytes.Buffer
	definition := catalog.Definition{
		Namespaces: map[string]catalog.Namespace{},
		Accounts:   map[string]catalog.Account{initialAccount.Name: initialAccount},
	}
	if err := catalog.Write(&catalogContents, definition); err != nil {
		return fmt.Errorf("encode catalog: %w", err)
	}
	paths := initializationPaths{directory: claim.directory, staging: claim.staging}
	if err := paths.writeStaged(catalogContents.Bytes(), metadataContents); err != nil {
		return err
	}
	return paths.commit()
}

type initializationPaths struct {
	directory string
	staging   string
}

func (paths initializationPaths) writeStaged(catalog, metadata []byte) error {
	if err := writeDurable(paths.catalogTemporary(), catalog); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	if err := writeDurable(paths.metadataTemporary(), metadata); err != nil {
		return fmt.Errorf("write instance metadata: %w", err)
	}
	return nil
}

func (paths initializationPaths) commit() (err error) {
	defer func() { err = joinInitializationErrors(err, paths.removeTemporary()) }()
	catalogInstalled, err := installStaged(paths.catalogTemporary(), paths.catalog())
	if err != nil {
		return joinInitializationErrors(fmt.Errorf("install catalog: %w", err), paths.removeInstalled(catalogInstalled, false))
	}
	metadataInstalled, err := installStaged(paths.metadataTemporary(), paths.metadata())
	if err != nil {
		return joinInitializationErrors(fmt.Errorf("install instance metadata: %w", err), paths.removeInstalled(catalogInstalled, metadataInstalled))
	}
	return nil
}

func installStaged(source, destination string) (bool, error) {
	if err := os.Link(source, destination); err != nil {
		return false, err
	}
	if err := os.Remove(source); err != nil {
		return true, err
	}
	return true, nil
}

func (paths initializationPaths) removeInstalled(catalog, metadata bool) error {
	var cleanup error
	if metadata {
		cleanup = joinInitializationErrors(cleanup, removeFile(paths.metadata()))
	}
	if catalog {
		cleanup = joinInitializationErrors(cleanup, removeFile(paths.catalog()))
	}
	return cleanup
}

func (paths initializationPaths) removeTemporary() error {
	return joinInitializationErrors(removeFile(paths.catalogTemporary()), removeFile(paths.metadataTemporary()))
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staged initialization artifact: %w", err)
	}
	return nil
}

func (paths initializationPaths) catalog() string {
	return filepath.Join(paths.directory, "catalog.json")
}

func (paths initializationPaths) metadata() string {
	return filepath.Join(paths.directory, "instance.json")
}

func (paths initializationPaths) catalogTemporary() string {
	return filepath.Join(paths.staging, "catalog.json")
}

func (paths initializationPaths) metadataTemporary() string {
	return filepath.Join(paths.staging, "instance.json")
}

func writeDurable(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(contents); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// ReadPassword obtains a secret from a named file or standard input and
// applies the password policy after one final LF or CRLF is removed.
func ReadPassword(file string, stdin io.Reader) (string, error) {
	var input []byte
	var err error
	if file != "" {
		input, err = os.ReadFile(file)
	} else {
		input, err = io.ReadAll(stdin)
	}
	if err != nil {
		return "", err
	}
	password := strings.TrimSuffix(string(input), "\n")
	password = strings.TrimSuffix(password, "\r")
	if err := credential.ValidatePassword(password); err != nil {
		return "", err
	}
	return password, nil
}
