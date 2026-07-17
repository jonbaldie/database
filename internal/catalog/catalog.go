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
	Name   string           `json:"name,omitempty"`
	Tables map[string]Table `json:"tables"`
}
type Table struct {
	Name        string     `json:"name,omitempty"`
	Columns     []string   `json:"columns"`
	ColumnTypes []string   `json:"column_types,omitempty"`
	Rows        [][]string `json:"rows,omitempty"`
}

// ColumnType reports the recorded logical type without inventing a fallback
// for catalog entries written before column types were persisted.
func (t Table) ColumnType(index int) (string, bool) {
	if index < 0 || index >= len(t.ColumnTypes) || strings.TrimSpace(t.ColumnTypes[index]) == "" {
		return "", false
	}
	return t.ColumnTypes[index], true
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
		return nil, errors.New("catalog namespaces are missing")
	}
	if err := validateDefinition(s.definition); err != nil {
		return nil, fmt.Errorf("invalid catalog: %w", err)
	}
	return s, nil
}

func validateDefinition(definition Definition) error {
	for namespaceName, namespace := range definition.Namespaces {
		if namespace.Tables == nil {
			return fmt.Errorf("namespace %q has no table registry", namespaceName)
		}
		for tableName, table := range namespace.Tables {
			if table.Columns == nil {
				return fmt.Errorf("table %q has no columns", tableName)
			}
			if len(table.ColumnTypes) != 0 && len(table.ColumnTypes) != len(table.Columns) {
				return fmt.Errorf("table %q has an invalid column type count", tableName)
			}
			for rowIndex, row := range table.Rows {
				if len(row) != len(table.Columns) {
					return fmt.Errorf("table %q row %d has an invalid column count", tableName, rowIndex)
				}
			}
		}
	}
	return nil
}

func (s *Store) CreateNamespace(name string) error {
	return s.mutate(func(definition *Definition) error {
		key := strings.ToLower(name)
		if _, ok := definition.Namespaces[key]; ok {
			return errors.New("namespace already exists")
		}
		definition.Namespaces[key] = Namespace{Name: name, Tables: map[string]Table{}}
		return nil
	})
}
func (s *Store) CreateTable(namespace, name string, columns []string) error {
	return s.CreateTableWithTypes(namespace, name, columns, nil)
}

// CreateTableWithTypes records the logical column types needed to reproduce a
// public schema definition. A nil type list preserves compatibility with the
// original catalog format, whose columns were names only.
func (s *Store) CreateTableWithTypes(namespace, name string, columns, columnTypes []string) error {
	return s.mutate(func(definition *Definition) error {
		key := strings.ToLower(namespace)
		ns, ok := definition.Namespaces[key]
		if !ok {
			return errors.New("namespace does not exist")
		}
		table := strings.ToLower(name)
		if _, ok := ns.Tables[table]; ok {
			return errors.New("table already exists")
		}
		tableDefinition := Table{Name: name, Columns: append([]string(nil), columns...)}
		if len(columnTypes) > 0 {
			if len(columnTypes) != len(columns) {
				return errors.New("column type count does not match column count")
			}
			tableDefinition.ColumnTypes = append([]string(nil), columnTypes...)
		}
		ns.Tables[table] = tableDefinition
		definition.Namespaces[key] = ns
		return nil
	})
}

func (s *Store) Insert(namespace, table string, row []string) error {
	return s.mutate(func(definition *Definition) error {
		ns, ok := definition.Namespaces[strings.ToLower(namespace)]
		if !ok {
			return errors.New("namespace does not exist")
		}
		key := strings.ToLower(table)
		tableDefinition, ok := ns.Tables[key]
		if !ok {
			return errors.New("table does not exist")
		}
		if len(row) != len(tableDefinition.Columns) {
			return errors.New("column count does not match value count")
		}
		tableDefinition.Rows = append(tableDefinition.Rows, append([]string(nil), row...))
		ns.Tables[key] = tableDefinition
		definition.Namespaces[strings.ToLower(namespace)] = ns
		return nil
	})
}

// ReplaceRows commits a complete, validated table-row image as one durable
// catalog mutation. Callers construct that image before this operation, so a
// malformed statement never exposes a partially changed table.
func (s *Store) ReplaceRows(namespace, table string, rows [][]string) error {
	return s.mutate(func(definition *Definition) error {
		key := strings.ToLower(namespace)
		ns, ok := definition.Namespaces[key]
		if !ok {
			return errors.New("namespace does not exist")
		}
		tableKey := strings.ToLower(table)
		current, ok := ns.Tables[tableKey]
		if !ok {
			return errors.New("table does not exist")
		}
		for index, row := range rows {
			if len(row) != len(current.Columns) {
				return fmt.Errorf("row %d column count does not match table", index)
			}
		}
		current.Rows = cloneRows(rows)
		ns.Tables[tableKey] = current
		definition.Namespaces[key] = ns
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
	staged := cloneDefinition(definition)
	if err := validateDefinition(staged); err != nil {
		return fmt.Errorf("invalid catalog: %w", err)
	}
	if err := s.persistLocked(staged); err != nil {
		return err
	}
	s.definition = staged
	return nil
}

func cloneDefinition(source Definition) Definition {
	copy := Definition{Namespaces: make(map[string]Namespace, len(source.Namespaces))}
	for namespace, value := range source.Namespaces {
		cloned := Namespace{Name: value.Name, Tables: make(map[string]Table, len(value.Tables))}
		for table, definition := range value.Tables {
			cloned.Tables[table] = Table{
				Name:        definition.Name,
				Columns:     append([]string(nil), definition.Columns...),
				ColumnTypes: append([]string(nil), definition.ColumnTypes...),
				Rows:        cloneRows(definition.Rows),
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

func (s *Store) mutate(action func(*Definition) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	staged := cloneDefinition(s.definition)
	if err := action(&staged); err != nil {
		return err
	}
	if err := validateDefinition(staged); err != nil {
		return fmt.Errorf("invalid catalog: %w", err)
	}
	if err := s.persistLocked(staged); err != nil {
		return err
	}
	s.definition = staged
	return nil
}

func (s *Store) persistLocked(definition Definition) error {
	b, err := catalogJSON(definition)
	if err != nil {
		return err
	}
	temporary, err := writeCatalogTemp(filepath.Dir(s.path), b)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, s.path); err != nil {
		return err
	}
	return syncCatalogDirectory(s.path)
}

func catalogJSON(definition Definition) ([]byte, error) {
	b, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func writeCatalogTemp(directory string, content []byte) (string, error) {
	file, err := os.CreateTemp(directory, ".catalog-*.tmp")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return "", err
	}
	if err := writeCatalogFile(file, content); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return "", err
	}
	return temporary, nil
}

func writeCatalogFile(file *os.File, content []byte) error {
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func syncCatalogDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
