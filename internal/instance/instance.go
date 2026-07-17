// Package instance owns the durable identity created by database init.
package instance

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Metadata struct {
	Schema       string `json:"schema"`
	InstanceID   string `json:"instance_id"`
	State        string `json:"state"`
	AdminAccount string `json:"admin_account"`
	PasswordHash string `json:"password_hash"`
}

func Load(directory string) (Metadata, error) {
	contents, err := os.ReadFile(filepath.Join(directory, "instance.json"))
	if err != nil {
		return Metadata{}, err
	}
	var metadata Metadata
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("decode instance metadata: %w", err)
	}
	if metadata.Schema != "database.instance/v1" || metadata.InstanceID == "" || metadata.AdminAccount == "" || metadata.PasswordHash == "" {
		return Metadata{}, errors.New("invalid instance metadata")
	}
	return metadata, nil
}

// Initialize creates one new, stopped instance in an empty directory.
func Initialize(directory, account, password string) (Metadata, error) {
	if err := validateInitializationInput(directory, account, password); err != nil {
		return Metadata{}, err
	}
	if err := prepareDirectory(directory); err != nil {
		return Metadata{}, err
	}
	metadata, err := newMetadata(account, password)
	if err != nil {
		return Metadata{}, err
	}
	if err := persistInitializedInstance(directory, metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func validateInitializationInput(directory, account, password string) error {
	if directory == "" || account == "" || password == "" {
		return errors.New("data directory, account, and password are required")
	}
	return nil
}

func prepareDirectory(directory string) error {
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}
		return nil
	}
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
	if len(entries) != 0 {
		return errors.New("data directory is not empty")
	}
	return nil
}

func newMetadata(account, password string) (Metadata, error) {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return Metadata{}, fmt.Errorf("generate instance identity: %w", err)
	}
	hash := sha256.Sum256([]byte(password))
	return Metadata{
		Schema:       "database.instance/v1",
		InstanceID:   hex.EncodeToString(idBytes[:]),
		State:        "stopped",
		AdminAccount: account,
		PasswordHash: hex.EncodeToString(hash[:]),
	}, nil
}

func persistInitializedInstance(directory string, metadata Metadata) error {
	metadataContents, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode instance metadata: %w", err)
	}
	metadataContents = append(metadataContents, '\n')
	paths := initializationPaths{directory: directory}
	if err := paths.writeStaged(metadataContents); err != nil {
		return err
	}
	return paths.commit()
}

type initializationPaths struct {
	directory string
}

func (paths initializationPaths) writeStaged(metadata []byte) error {
	if err := writeDurable(paths.catalogTemporary(), []byte("{\n  \"namespaces\": {}\n}\n")); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	if err := writeDurable(paths.metadataTemporary(), metadata); err != nil {
		_ = os.Remove(paths.catalogTemporary())
		return fmt.Errorf("write instance metadata: %w", err)
	}
	return nil
}

func (paths initializationPaths) commit() error {
	defer paths.removeTemporary()
	if err := os.Rename(paths.catalogTemporary(), paths.catalog()); err != nil {
		return fmt.Errorf("install catalog: %w", err)
	}
	if err := os.Rename(paths.metadataTemporary(), paths.metadata()); err != nil {
		_ = os.Remove(paths.catalog())
		return fmt.Errorf("install instance metadata: %w", err)
	}
	return nil
}

func (paths initializationPaths) removeTemporary() {
	_ = os.Remove(paths.catalogTemporary())
	_ = os.Remove(paths.metadataTemporary())
}

func (paths initializationPaths) catalog() string {
	return filepath.Join(paths.directory, "catalog.json")
}

func (paths initializationPaths) metadata() string {
	return filepath.Join(paths.directory, "instance.json")
}

func (paths initializationPaths) catalogTemporary() string { return paths.catalog() + ".initializing" }

func (paths initializationPaths) metadataTemporary() string {
	return paths.metadata() + ".initializing"
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

// ReadPassword obtains a secret from a named file or standard input.
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
	if password == "" {
		return "", errors.New("password is empty")
	}
	return password, nil
}
