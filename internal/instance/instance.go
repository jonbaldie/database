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

// Initialize creates one new, stopped instance in an empty directory.
func Initialize(directory, account, password string) (Metadata, error) {
	if directory == "" || account == "" || password == "" {
		return Metadata{}, errors.New("data directory, account, and password are required")
	}
	if info, err := os.Stat(directory); err == nil {
		if !info.IsDir() {
			return Metadata{}, errors.New("data directory is not a directory")
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return Metadata{}, fmt.Errorf("inspect data directory: %w", err)
		}
		if len(entries) != 0 {
			return Metadata{}, errors.New("data directory is not empty")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Metadata{}, fmt.Errorf("inspect data directory: %w", err)
	} else if err := os.MkdirAll(directory, 0o700); err != nil {
		return Metadata{}, fmt.Errorf("create data directory: %w", err)
	}

	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return Metadata{}, fmt.Errorf("generate instance identity: %w", err)
	}
	hash := sha256.Sum256([]byte(password))
	metadata := Metadata{
		Schema:       "database.instance/v1",
		InstanceID:   hex.EncodeToString(idBytes[:]),
		State:        "stopped",
		AdminAccount: account,
		PasswordHash: hex.EncodeToString(hash[:]),
	}
	contents, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return Metadata{}, fmt.Errorf("encode instance metadata: %w", err)
	}
	contents = append(contents, '\n')
	if err := writeDurable(filepath.Join(directory, "instance.json"), contents); err != nil {
		return Metadata{}, fmt.Errorf("write instance metadata: %w", err)
	}
	if err := writeDurable(filepath.Join(directory, "catalog.json"), []byte("{\n  \"namespaces\": {}\n}\n")); err != nil {
		return Metadata{}, fmt.Errorf("write catalog: %w", err)
	}
	return metadata, nil
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
