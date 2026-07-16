// Package catalog owns the durable logical namespace and table registry.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Definition struct {
	Namespaces map[string]Namespace `json:"namespaces"`
}
type Namespace struct {
	Tables map[string]Table `json:"tables"`
}
type Table struct {
	Columns []string `json:"columns"`
}
type Store struct {
	mu         sync.Mutex
	path       string
	definition Definition
}

func Open(directory string) (*Store, error) {
	s := &Store{path: filepath.Join(directory, "catalog.json"), definition: Definition{Namespaces: map[string]Namespace{}}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.definition); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if s.definition.Namespaces == nil {
		s.definition.Namespaces = map[string]Namespace{}
	}
	return s, nil
}

func (s *Store) CreateNamespace(name string) error {
	return s.mutate(func() error {
		key := strings.ToLower(name)
		if _, ok := s.definition.Namespaces[key]; ok {
			return errors.New("namespace already exists")
		}
		s.definition.Namespaces[key] = Namespace{Tables: map[string]Table{}}
		return nil
	})
}
func (s *Store) CreateTable(namespace, name string, columns []string) error {
	return s.mutate(func() error {
		key := strings.ToLower(namespace)
		ns, ok := s.definition.Namespaces[key]
		if !ok {
			return errors.New("namespace does not exist")
		}
		table := strings.ToLower(name)
		if _, ok := ns.Tables[table]; ok {
			return errors.New("table already exists")
		}
		ns.Tables[table] = Table{Columns: append([]string(nil), columns...)}
		s.definition.Namespaces[key] = ns
		return nil
	})
}
func (s *Store) Snapshot() Definition { s.mu.Lock(); defer s.mu.Unlock(); return s.definition }
func (s *Store) mutate(action func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := action(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.definition, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(s.path, b, 0o600)
}
