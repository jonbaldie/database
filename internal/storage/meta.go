package storage

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// EnsureTable records schema for a table. It is idempotent and updates shape.
func (e *Engine) EnsureTable(namespace, name string, columns, primary []string, uniques [][]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errClosed
	}
	key := tableKey(namespace, name)
	if current, ok := e.tables[key]; ok {
		current.columns = append([]string(nil), columns...)
		current.primary = append([]string(nil), primary...)
		current.uniques = cloneStringMatrix(uniques)
		current.uniqueIdx = map[string]map[string]int{}
		for _, unique := range current.uniques {
			current.uniqueIdx[strings.Join(unique, "\x00")] = map[string]int{}
		}
		rebuildIndexes(current)
		return e.persistMetaLocked()
	}
	created := newTable(namespace, name, columns, primary, uniques)
	e.tables[key] = created
	return e.persistMetaLocked()
}

func rebuildIndexes(current *table) {
	current.primaryIdx = map[string]int{}
	for _, unique := range current.uniques {
		current.uniqueIdx[strings.Join(unique, "\x00")] = map[string]int{}
	}
	for index, row := range current.rows {
		if len(row) < len(current.columns) {
			padded := make([]string, len(current.columns))
			copy(padded, row)
			current.rows[index] = padded
			row = padded
		}
		if len(current.primary) > 0 {
			current.primaryIdx[current.primaryKey(row)] = index
		}
		for _, unique := range current.uniques {
			indexKey := strings.Join(unique, "\x00")
			current.uniqueIdx[indexKey][rowKey(row, current.columnIndexes(unique))] = index
		}
	}
}

func newTable(namespace, name string, columns, primary []string, uniques [][]string) *table {
	uniqueIdx := map[string]map[string]int{}
	for _, unique := range uniques {
		uniqueIdx[strings.Join(unique, "\x00")] = map[string]int{}
	}
	return &table{
		namespace:  namespace,
		name:       name,
		columns:    append([]string(nil), columns...),
		primary:    append([]string(nil), primary...),
		uniques:    cloneStringMatrix(uniques),
		primaryIdx: map[string]int{},
		uniqueIdx:  uniqueIdx,
	}
}

func (e *Engine) persistMetaLocked() error {
	var builder strings.Builder
	for _, current := range e.tables {
		builder.WriteString(current.namespace)
		builder.WriteByte('\t')
		builder.WriteString(current.name)
		builder.WriteByte('\t')
		builder.WriteString(strings.Join(current.columns, ","))
		builder.WriteByte('\t')
		builder.WriteString(strings.Join(current.primary, ","))
		builder.WriteByte('\t')
		for index, unique := range current.uniques {
			if index > 0 {
				builder.WriteByte(';')
			}
			builder.WriteString(strings.Join(unique, ","))
		}
		builder.WriteByte('\n')
	}
	temporary := rowsMetaPath(e.directory) + ".tmp"
	if err := os.WriteFile(temporary, []byte(builder.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, rowsMetaPath(e.directory))
}

func (e *Engine) loadMeta() error {
	content, err := os.ReadFile(rowsMetaPath(e.directory))
	if errorsIsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			return fmt.Errorf("invalid table meta line")
		}
		uniques := [][]string{}
		if len(parts) > 4 && parts[4] != "" {
			for _, group := range strings.Split(parts[4], ";") {
				uniques = append(uniques, splitCSV(group))
			}
		}
		created := newTable(parts[0], parts[1], splitCSV(parts[2]), splitCSV(parts[3]), uniques)
		e.tables[tableKey(parts[0], parts[1])] = created
	}
	return nil
}

func (e *Engine) loadCheckpoints() error {
	for _, current := range e.tables {
		path := rowsCheckpointPath(e.directory, current.namespace, current.name)
		file, err := os.Open(path)
		if errorsIsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := loadCheckpoint(file, current); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

func loadCheckpoint(reader io.Reader, current *table) error {
	buffered := bufio.NewReader(reader)
	for {
		row, err := readRow(buffered)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := current.appendRow(row); err != nil {
			return err
		}
	}
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneStringMatrix(values [][]string) [][]string {
	if values == nil {
		return nil
	}
	copy := make([][]string, len(values))
	for index, row := range values {
		copy[index] = append([]string(nil), row...)
	}
	return copy
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
