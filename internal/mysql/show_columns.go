package mysql

import (
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

// describeTarget reports the table target of a DESCRIBE or DESC statement.
// Both spellings share SHOW COLUMNS' plain shape.
func describeTarget(query, lower string) (string, bool) {
	value := strings.TrimSpace(query)
	for _, prefix := range []string{"describe ", "desc "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(value[len(prefix):]), true
		}
	}
	if lower == "describe" || lower == "desc" {
		return "", true
	}
	return "", false
}

func (s *catalogExecutor) describe(query, target string) (*queryResult, error) {
	projection := catalogMetadataForSession(s.session)
	_, table, err := s.resolveShowTable(target, "", projection)
	if err != nil {
		return nil, err
	}
	return showColumns(table, false, ""), nil
}

// showColumnsAndStatusStatement dispatches the column-structure and server
// counter statements: SHOW COLUMNS and SHOW STATUS.
func showColumnsAndStatusStatement(s *catalogExecutor, query, lower string) (*queryResult, bool, error) {
	switch {
	case isShowColumnsStatement(lower):
		result, err := s.showColumnsStatement(query, lower)
		return result, true, err
	case isShowStatusStatement(lower):
		result, err := s.showStatus(query, lower)
		return result, true, err
	default:
		return nil, false, nil
	}
}

// showColumnsTarget reports the raw text after a SHOW COLUMNS, SHOW FIELDS,
// or their FULL variants, and whether the full shape was requested.
func showColumnsTarget(query, lower string) (string, bool) {
	value := strings.TrimSpace(query)
	for _, prefix := range []struct {
		text string
		full bool
	}{
		{text: "show full columns from ", full: true},
		{text: "show full columns in ", full: true},
		{text: "show full fields from ", full: true},
		{text: "show full fields in ", full: true},
		{text: "show columns from "},
		{text: "show columns in "},
		{text: "show fields from "},
		{text: "show fields in "},
	} {
		if strings.HasPrefix(lower, prefix.text) {
			return value[len(prefix.text):], prefix.full
		}
	}
	return "", false
}

// isShowColumnsStatement reports whether the statement is a SHOW COLUMNS or
// SHOW FIELDS form, with or without the FULL keyword.
func isShowColumnsStatement(lower string) bool {
	return hasAnyPrefix(lower, "show columns ", "show fields ", "show full columns ", "show full fields ")
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func (s *catalogExecutor) showColumnsStatement(query, lower string) (*queryResult, error) {
	remainder, full := showColumnsTarget(query, lower)
	modifiers, err := parseShowModifiers(remainder, true)
	if err != nil {
		return nil, err
	}
	projection := catalogMetadataForSession(s.session)
	namespaceName, table, err := s.resolveShowTableParts(modifiers.objectParts, modifiers.namespace, projection)
	if err != nil {
		return nil, err
	}
	result := showColumns(table, full, projection.columnPrivileges(namespaceName))
	return applyShowFilters(s.session, result, modifiers)
}

func (s *catalogExecutor) resolveShowTable(target, namespaceOverride string, projection catalogMetadataProjection) (string, catalogTableMetadata, error) {
	parts, rest, ok := consumeQualifiedIdentifier(strings.TrimSpace(target))
	if !ok || strings.TrimSpace(rest) != "" {
		return "", catalogTableMetadata{}, sqlFailure{1064, "42000", "invalid table name"}
	}
	return s.resolveShowTableParts(parts, namespaceOverride, projection)
}

func (s *catalogExecutor) resolveShowTableParts(parts []string, namespaceOverride string, projection catalogMetadataProjection) (string, catalogTableMetadata, error) {
	if len(parts) == 0 || len(parts) > 2 {
		return "", catalogTableMetadata{}, sqlFailure{1064, "42000", "invalid table name"}
	}
	if namespaceOverride != "" {
		parts = []string{namespaceOverride, parts[len(parts)-1]}
	}
	namespaceName, tableName, err := s.qualifiedShowTableTarget("", parts)
	if err != nil {
		return "", catalogTableMetadata{}, err
	}
	table, err := projection.table(namespaceName, tableName)
	if err != nil {
		return "", catalogTableMetadata{}, err
	}
	return namespaceName, table, nil
}

// isShowStatusStatement reports whether the statement is a SHOW STATUS form.
// Scope keywords are accepted because this catalog's counters are server-wide,
// so the session and global shapes agree.
func isShowStatusStatement(lower string) bool {
	return lower == "show status" || lower == "show session status" || lower == "show global status" ||
		hasAnyPrefix(lower, "show status ", "show session status ", "show global status ")
}

// showStatus publishes the non-sensitive server counters that diagnostics
// already exposes, sorted by name so clients see stable output.
func (s *catalogExecutor) showStatus(query, lower string) (*queryResult, error) {
	usage := s.server.resources.usage()
	counters := []struct {
		name  string
		value int64
	}{
		{"cancellation_count", usage.CancellationCount},
		{"execution_memory_bytes", usage.ExecutionMemoryBytes},
		{"memory_exhaustion_count", usage.MemoryExhaustionCount},
		{"peak_execution_memory_bytes", usage.PeakExecutionMemoryBytes},
		{"peak_temporary_storage_bytes", usage.PeakTemporaryStorageBytes},
		{"spill_bytes", usage.SpillBytes},
		{"spill_count", usage.SpillCount},
		{"temporary_exhaustion_count", usage.TemporaryExhaustionCount},
		{"temporary_storage_bytes", usage.TemporaryStorageBytes},
		{"timeout_count", usage.TimeoutCount},
	}
	result := &queryResult{columns: []string{"Variable_name", "Value"}}
	for _, counter := range counters {
		result.rows = append(result.rows, []string{counter.name, strconv.FormatInt(counter.value, 10)})
	}
	return filterShowLike(result, query, lower), nil
}

// showColumns renders the per-column structure one DESCRIBE or SHOW COLUMNS
// statement reports. The full shape adds the collation, privilege, and comment
// columns this catalog does not track; collation and comment stay honestly
// NULL or empty, and privileges project the account's namespace grants.
func showColumns(table catalogTableMetadata, full bool, privileges string) *queryResult {
	columns := []string{"Field", "Type", "Null", "Key", "Default", "Extra"}
	if full {
		columns = []string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}
	}
	rows := make([][]string, 0, len(table.columns))
	nulls := make([][]bool, 0, len(table.columns))
	for _, column := range table.columns {
		row, null := column.showRow(full, privileges)
		rows = append(rows, row)
		nulls = append(nulls, null)
	}
	return &queryResult{columns: columns, rows: rows, nulls: nulls}
}

func (column catalogColumnMetadata) showRow(full bool, privileges string) ([]string, []bool) {
	typeName := ""
	if !column.columnType.null {
		typeName = column.columnType.value
	}
	nullability := "NO"
	if column.attribute.Nullable {
		nullability = "YES"
	}
	row := []string{column.name, typeName, nullability, column.key, column.defaultValue.value, column.extra}
	nulls := []bool{false, false, false, false, column.defaultValue.null, false}
	if !full {
		return row, nulls
	}
	return []string{column.name, typeName, "", nullability, column.key, column.defaultValue.value, column.extra, privileges, ""},
		[]bool{false, false, true, false, false, column.defaultValue.null, false, false, false}
}

// showColumnKey follows the MySQL rule: PRI for any primary key column, UNI
// for the first column of a unique index that cannot hold NULL, MUL for the
// first column of any other index, and empty otherwise.
func showColumnKey(table catalog.Table, columnIndex int) string {
	column := table.Columns[columnIndex]
	if showColumnPrimaryKey(table, column) {
		return "PRI"
	}
	return showColumnSecondaryKey(table, column, columnIndex)
}

// showColumnPrimaryKey reports whether the column belongs to the primary key.
func showColumnPrimaryKey(table catalog.Table, column string) bool {
	for _, constraint := range table.Constraints {
		if constraint.Type == catalog.ConstraintTypePrimary && constraintContainsColumn(constraint, column) {
			return true
		}
	}
	return false
}

// showColumnSecondaryKey applies the MySQL UNI and MUL rule to a column the
// primary key does not cover.
func showColumnSecondaryKey(table catalog.Table, column string, columnIndex int) string {
	attribute := catalog.ColumnAttributeAt(table, columnIndex)
	for _, index := range effectiveTableIndexes(table) {
		if len(index.Parts) == 0 || index.Parts[0].Column == "" || !identifiersEqual(index.Parts[0].Column, column) {
			continue
		}
		if index.Unique && !attribute.Nullable {
			return "UNI"
		}
		return "MUL"
	}
	return ""
}

func constraintContainsColumn(constraint catalog.Constraint, column string) bool {
	for _, name := range constraint.Columns {
		if identifiersEqual(name, column) {
			return true
		}
	}
	return false
}
