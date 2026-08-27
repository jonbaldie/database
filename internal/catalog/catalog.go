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
	Accounts   map[string]Account   `json:"accounts,omitempty"`
}

// Account is durable authentication and authorization state. PasswordHash is
// an opaque verifier and is never part of a SQL result.
type Account struct {
	Name         string  `json:"name"`
	PasswordHash string  `json:"password_hash"`
	Locked       bool    `json:"locked,omitempty"`
	Grants       []Grant `json:"grants,omitempty"`
}

// Grant is one finite v0.1 privilege. Namespace is empty for server grants.
type Grant struct {
	Privilege string `json:"privilege"`
	Namespace string `json:"namespace,omitempty"`
}
type Namespace struct {
	Name   string           `json:"name,omitempty"`
	Tables map[string]Table `json:"tables"`
}
type Table struct {
	Name             string            `json:"name,omitempty"`
	Columns          []string          `json:"columns"`
	ColumnTypes      []string          `json:"column_types,omitempty"`
	ColumnAttributes []ColumnAttribute `json:"column_attributes,omitempty"`
	Constraints      []Constraint      `json:"constraints,omitempty"`
	Indexes          []Index           `json:"indexes,omitempty"`
	Rows             [][]string        `json:"rows,omitempty"`
	PrimaryIndex     map[string]int    `json:"-"`
}

// Index is one declared B-tree access path. Primary and unique constraints
// remain constraints and are exposed as effective indexes by the SQL layer.
type Index struct {
	Name      string      `json:"name"`
	Unique    bool        `json:"unique,omitempty"`
	Parts     []IndexPart `json:"parts"`
	Invisible bool        `json:"invisible,omitempty"`
	Comment   string      `json:"comment,omitempty"`
}

// IndexPart is one ordered column or expression in an index definition.
type IndexPart struct {
	Column       string `json:"column,omitempty"`
	Expression   string `json:"expression,omitempty"`
	PrefixLength int    `json:"prefix_length,omitempty"`
	Descending   bool   `json:"descending,omitempty"`
}

// ColumnAttribute records rules that belong to one column. An absent attribute
// list is compatible with catalogs written before column rules were supported.
type ColumnAttribute struct {
	Nullable   bool   `json:"nullable"`
	HasDefault bool   `json:"has_default,omitempty"`
	Default    string `json:"default,omitempty"`
}

const (
	ConstraintTypePrimary    = "primary"
	ConstraintTypeUnique     = "unique"
	ConstraintTypeCheck      = "check"
	ConstraintTypeForeignKey = "foreign_key"
)

// Constraint records a durable table constraint. Values are SQL identifiers or
// canonical SQL values; enforcement belongs to the SQL server layer.
type Constraint struct {
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	Columns             []string `json:"columns,omitempty"`
	Check               string   `json:"check,omitempty"`
	ReferencedNamespace string   `json:"referenced_namespace,omitempty"`
	ReferencedTable     string   `json:"referenced_table,omitempty"`
	ReferencedColumns   []string `json:"referenced_columns,omitempty"`
}

// ErrRevisionConflict reports that a concurrent catalog commit superseded the
// snapshot a caller tried to publish.
var ErrRevisionConflict = errors.New("catalog changed concurrently")

// ColumnType reports the recorded logical type without inventing a fallback
// for catalog entries written before column types were persisted.
func (t Table) ColumnType(index int) (string, bool) {
	if index < 0 || index >= len(t.ColumnTypes) || strings.TrimSpace(t.ColumnTypes[index]) == "" {
		return "", false
	}
	return t.ColumnTypes[index], true
}

// ColumnAttributeAt reports the recorded column rule. Older tables have
// nullable columns and no default value.
func ColumnAttributeAt(t Table, index int) ColumnAttribute {
	if index < 0 || index >= len(t.ColumnAttributes) {
		return ColumnAttribute{Nullable: true}
	}
	return t.ColumnAttributes[index]
}

type Store struct {
	mu               sync.Mutex
	path             string
	definition       Definition
	revision         uint64
	rows             rowEngine
	writerOnce       sync.Once
	writes           chan *writeRequest
	publishValidator func(previous, next Definition) error
}

// SetPublishValidator installs the SQL constraint check used before durable
// publication. The catalog package only validates structural shape itself.
func (s *Store) SetPublishValidator(validator func(previous, next Definition) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishValidator = validator
}

// rowEngine is the durable row image used beside schema metadata.
type rowEngine interface {
	EnsureTable(namespace, name string, columns, primary []string, uniques [][]string) error
	Begin() (rowTxn, error)
	LookupPrimary(namespace, name, key string) ([]string, bool)
	LookupUnique(namespace, name, column, key string) ([]string, bool)
	SnapshotRows(namespace, name string) ([][]string, bool)
	Close() error
}

type rowTxn interface {
	Insert(namespace, name string, row []string) error
	UpdatePrimary(namespace, name, primary string, row []string) error
	DeletePrimary(namespace, name, primary string) error
	Clear(namespace, name string) error
	Commit() error
}

func Open(directory string) (*Store, error) {
	s := &Store{path: filepath.Join(directory, "catalog.json"), definition: Definition{Namespaces: map[string]Namespace{}, Accounts: map[string]Account{}}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return openWithRows(s, directory)
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
	if s.definition.Accounts == nil {
		s.definition.Accounts = map[string]Account{}
	}
	if err := validateDefinition(s.definition); err != nil {
		return nil, fmt.Errorf("invalid catalog: %w", err)
	}
	return openWithRows(s, directory)
}

// Recover removes abandoned catalog snapshots left by an interrupted commit.
// Callers must hold the data-directory ownership claim before invoking it.
func Recover(directory string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".catalog-") || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed = true
	}
	if !removed {
		return nil
	}
	return syncCatalogDirectory(filepath.Join(directory, "catalog.json"))
}

func validateDefinition(definition Definition) error {
	for namespaceName, namespace := range definition.Namespaces {
		if namespace.Tables == nil {
			return fmt.Errorf("namespace %q has no table registry", namespaceName)
		}
		for tableName, table := range namespace.Tables {
			if err := validateTableDefinition(tableName, table); err != nil {
				return err
			}
		}
	}
	for key, account := range definition.Accounts {
		if key != account.Name || account.Name == "" || account.PasswordHash == "" {
			return fmt.Errorf("invalid account %q", key)
		}
	}
	return nil
}

func validateTableDefinition(tableName string, table Table) error {
	if table.Columns == nil {
		return fmt.Errorf("table %q has no columns", tableName)
	}
	if len(table.ColumnTypes) != 0 && len(table.ColumnTypes) != len(table.Columns) {
		return fmt.Errorf("table %q has an invalid column type count", tableName)
	}
	if len(table.ColumnAttributes) != 0 && len(table.ColumnAttributes) != len(table.Columns) {
		return fmt.Errorf("table %q has an invalid column attribute count", tableName)
	}
	return validateTableRows(tableName, table)
}

func validateTableRows(tableName string, table Table) error {
	for rowIndex, row := range table.Rows {
		if len(row) != len(table.Columns) {
			return fmt.Errorf("table %q row %d has an invalid column count", tableName, rowIndex)
		}
	}
	return nil
}

func (s *Store) CreateNamespace(name string) error {
	return s.mutate(func(definition *Definition) error {
		key := Key(name)
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
		key := Key(namespace)
		ns, ok := definition.Namespaces[key]
		if !ok {
			return errors.New("namespace does not exist")
		}
		table := Key(name)
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
		ns, ok := definition.Namespaces[Key(namespace)]
		if !ok {
			return errors.New("namespace does not exist")
		}
		key := Key(table)
		tableDefinition, ok := ns.Tables[key]
		if !ok {
			return errors.New("table does not exist")
		}
		if len(row) != len(tableDefinition.Columns) {
			return errors.New("column count does not match value count")
		}
		tableDefinition.Rows = append(tableDefinition.Rows, append([]string(nil), row...))
		ns.Tables[key] = tableDefinition
		definition.Namespaces[Key(namespace)] = ns
		return nil
	})
}

// ReplaceRows commits a complete, validated table-row image as one durable
// catalog mutation. Callers construct that image before this operation, so a
// malformed statement never exposes a partially changed table.
func (s *Store) ReplaceRows(namespace, table string, rows [][]string) error {
	return s.mutate(func(definition *Definition) error {
		key := Key(namespace)
		ns, ok := definition.Namespaces[key]
		if !ok {
			return errors.New("namespace does not exist")
		}
		tableKey := Key(table)
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
	definition, _ := s.SnapshotWithRevision()
	return definition
}

// SnapshotWithRevision returns one immutable view and the revision that owns
// it. The revision is process-local and lets a transaction reject a stale
// whole-catalog replacement instead of silently overwriting another commit.
func (s *Store) SnapshotWithRevision() (Definition, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneDefinition(s.definition), s.revision
}

// Replace atomically restores a previously captured catalog snapshot.
func (s *Store) Replace(definition Definition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaceLocked(definition)
}

// ReplaceIfRevision publishes definition only if expected is still current.
// It is the commit gate for session-local transaction snapshots.
func (s *Store) ReplaceIfRevision(expected uint64, definition Definition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != expected {
		return ErrRevisionConflict
	}
	return s.replaceLocked(definition)
}

// Apply validates an isolated catalog transformation without publishing it.
// Transaction sessions use this to keep uncommitted work private.
func Apply(definition Definition, action func(*Definition) error) (Definition, error) {
	staged := cloneDefinition(definition)
	if err := action(&staged); err != nil {
		return Definition{}, err
	}
	if err := validateDefinition(staged); err != nil {
		return Definition{}, fmt.Errorf("invalid catalog: %w", err)
	}
	return staged, nil
}

func cloneDefinition(source Definition) Definition {
	copy := Definition{Namespaces: make(map[string]Namespace, len(source.Namespaces)), Accounts: make(map[string]Account, len(source.Accounts))}
	for namespace, value := range source.Namespaces {
		cloned := Namespace{Name: value.Name, Tables: make(map[string]Table, len(value.Tables))}
		for table, definition := range value.Tables {
			cloned.Tables[table] = Table{
				Name:             definition.Name,
				Columns:          append([]string(nil), definition.Columns...),
				ColumnTypes:      append([]string(nil), definition.ColumnTypes...),
				ColumnAttributes: append([]ColumnAttribute(nil), definition.ColumnAttributes...),
				Constraints:      CloneConstraints(definition.Constraints),
				Indexes:          CloneIndexes(definition.Indexes),
				// Row images and primary indexes are immutable after publication.
				// Snapshots share both so validators and point lookups stay O(1);
				// writers copy the index before mutating it.
				Rows:         sharedRowImage(definition.Rows),
				PrimaryIndex: definition.PrimaryIndex,
			}
		}
		copy.Namespaces[namespace] = cloned
	}
	for name, account := range source.Accounts {
		copy.Accounts[name] = Account{Name: account.Name, PasswordHash: account.PasswordHash, Locked: account.Locked, Grants: append([]Grant(nil), account.Grants...)}
	}
	return copy
}

// CloneConstraints returns a copy that owns its column lists.
func CloneConstraints(constraints []Constraint) []Constraint {
	copy := make([]Constraint, len(constraints))
	for index, constraint := range constraints {
		copy[index] = constraint
		copy[index].Columns = append([]string(nil), constraint.Columns...)
		copy[index].ReferencedColumns = append([]string(nil), constraint.ReferencedColumns...)
	}
	return copy
}

// CloneIndexes returns a copy that owns every index part list.
func CloneIndexes(indexes []Index) []Index {
	copy := make([]Index, len(indexes))
	for index, definition := range indexes {
		copy[index] = definition
		copy[index].Parts = append([]IndexPart(nil), definition.Parts...)
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

func sharedRowImage(rows [][]string) [][]string {
	if rows == nil {
		return nil
	}
	return rows[:len(rows):len(rows)]
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
	if err := s.syncRowsLocked(s.definition, staged); err != nil {
		return err
	}
	if err := s.persistLocked(staged); err != nil {
		return err
	}
	s.definition = staged
	s.revision++
	return nil
}

func sameSchema(left, right Definition) bool {
	if len(left.Namespaces) != len(right.Namespaces) || len(left.Accounts) != len(right.Accounts) {
		return false
	}
	for key, namespace := range left.Namespaces {
		other, ok := right.Namespaces[key]
		if !ok || len(namespace.Tables) != len(other.Tables) {
			return false
		}
		for tableKey, table := range namespace.Tables {
			otherTable, ok := other.Tables[tableKey]
			if !ok || !sameSchemaTable(table, otherTable) {
				return false
			}
		}
	}
	return true
}

func sameSchemaTable(left, right Table) bool {
	if len(left.Columns) != len(right.Columns) || len(left.ColumnTypes) != len(right.ColumnTypes) {
		return false
	}
	if len(left.Constraints) != len(right.Constraints) || len(left.Indexes) != len(right.Indexes) {
		return false
	}
	for index := range left.Columns {
		if left.Columns[index] != right.Columns[index] {
			return false
		}
	}
	return true
}

func (s *Store) persistLocked(definition Definition) error {
	b, err := catalogJSON(SchemaOnly(definition))
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

// SchemaOnly returns a catalog definition with table row images removed so
// callers can compare durable schema snapshots with online backup captures.
func SchemaOnly(definition Definition) Definition {
	stripped := cloneDefinition(definition)
	for namespaceKey, namespace := range stripped.Namespaces {
		for tableKey, table := range namespace.Tables {
			table.Rows = nil
			namespace.Tables[tableKey] = table
		}
		stripped.Namespaces[namespaceKey] = namespace
	}
	return stripped
}

// Encode serializes one catalog definition in the durable on-disk JSON shape.
func Encode(definition Definition) ([]byte, error) {
	return catalogJSON(definition)
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
