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
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows,omitempty"`
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

func (s *Store) Insert(namespace, table string, row []string) error {
	return s.mutate(func() error {
		ns, ok := s.definition.Namespaces[strings.ToLower(namespace)]
		if !ok {
			return errors.New("namespace does not exist")
		}
		key := strings.ToLower(table)
		definition, ok := ns.Tables[key]
		if !ok {
			return errors.New("table does not exist")
		}
		if len(row) != len(definition.Columns) {
			return errors.New("column count does not match value count")
		}
		definition.Rows = append(definition.Rows, append([]string(nil), row...))
		ns.Tables[key] = definition
		s.definition.Namespaces[strings.ToLower(namespace)] = ns
		return nil
	})
}

func (s *Store) Snapshot() Definition {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneDefinition(s.definition)
}

// Replace atomically restores a previously captured catalog snapshot.
func (s *Store) Replace(definition Definition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.definition = cloneDefinition(definition)
	return s.persistLocked()
}

func cloneDefinition(source Definition) Definition {
	copy := Definition{Namespaces: make(map[string]Namespace, len(source.Namespaces))}
	for namespace, value := range source.Namespaces {
		cloned := Namespace{Tables: make(map[string]Table, len(value.Tables))}
		for table, definition := range value.Tables {
			cloned.Tables[table] = Table{
				Columns: append([]string(nil), definition.Columns...),
				Rows:    cloneRows(definition.Rows),
			}
		}
		copy.Namespaces[namespace] = cloned
	}
	return copy
}

func cloneRows(rows [][]string) [][]string {
	copy := make([][]string, len(rows))
	for index, row := range rows {
		copy[index] = append([]string(nil), row...)
	}
	return copy
}

func (s *Store) mutate(action func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := action(); err != nil {
		return err
	}
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	b, err := json.MarshalIndent(s.definition, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(b); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
