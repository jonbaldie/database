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
	_, table, err := s.resolveShowTable(query, target)
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
	target := strings.TrimSpace(remainder)
	if index := strings.Index(strings.ToLower(remainder), " like "); index >= 0 {
		target = strings.TrimSpace(remainder[:index])
	}
	namespaceName, table, err := s.resolveShowTable(query, target)
	if err != nil {
		return nil, err
	}
	result := showColumns(table, full, s.columnPrivileges(namespaceName))
	return filterShowLike(result, query, lower), nil
}

// resolveShowTable resolves the table named by a catalog SHOW or DESCRIBE
// target against the statement-scoped catalog snapshot.
func (s *catalogExecutor) resolveShowTable(query, target string) (string, catalog.Table, error) {
	parts, valid := splitQualifiedIdentifier(target)
	if !valid || len(parts) > 2 {
		return "", catalog.Table{}, sqlFailure{1064, "42000", "invalid table name"}
	}
	namespaceName, tableName, err := s.qualifiedShowTableTarget(target, parts)
	if err != nil {
		return "", catalog.Table{}, err
	}
	namespace, ok := s.metadataDefinition().Namespaces[catalog.Key(namespaceName)]
	if !ok {
		return "", catalog.Table{}, metadataNamespaceFailure(s.server.config.Catalog, namespaceName)
	}
	table, ok := namespace.Tables[catalog.Key(tableName)]
	if !ok {
		return "", catalog.Table{}, sqlFailure{1146, "42S02", "table '" + namespaceName + "." + tableName + "' doesn't exist"}
	}
	if table.Name == "" {
		table.Name = strings.ToLower(tableName)
	}
	return namespaceName, table, nil
}

// columnPrivileges follows the information_schema.COLUMNS.PRIVILEGES contract:
// it projects the current account's namespace grants into column capabilities.
func (s *catalogExecutor) columnPrivileges(namespaceName string) string {
	privileges := []string{}
	for _, grant := range []struct {
		privilege string
		spellings []string
	}{
		{privilege: "DATA_READ", spellings: []string{"select"}},
		{privilege: "DATA_WRITE", spellings: []string{"insert", "update"}},
		{privilege: "SCHEMA_MANAGEMENT", spellings: []string{"references"}},
	} {
		if s.requireGrant(grant.privilege, namespaceName) != nil {
			continue
		}
		privileges = append(privileges, grant.spellings...)
	}
	return strings.Join(privileges, ",")
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
func showColumns(table catalog.Table, full bool, privileges string) *queryResult {
	columns := []string{"Field", "Type", "Null", "Key", "Default", "Extra"}
	if full {
		columns = []string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}
	}
	rows := make([][]string, 0, len(table.Columns))
	nulls := make([][]bool, 0, len(table.Columns))
	for index, column := range table.Columns {
		attribute := catalog.ColumnAttributeAt(table, index)
		columnType, _ := table.ColumnType(index)
		defaultCell, defaultNull := "", true
		if attribute.HasDefault && attribute.Default != storedSQLNullValue {
			defaultCell, defaultNull = attribute.Default, false
		}
		shared := []string{
			column,
			columnType,
			nullLabel(attribute),
			showColumnKey(table, index),
			defaultCell,
			showColumnExtra(attribute),
		}
		sharedNull := []bool{false, false, false, false, defaultNull, false}
		row, null := shared, sharedNull
		if full {
			row = append([]string{column, columnType, ""}, shared[2:]...)
			row = append(row, privileges, "")
			null = append([]bool{false, false, true}, sharedNull[2:]...)
			null = append(null, false, false)
		}
		rows = append(rows, row)
		nulls = append(nulls, null)
	}
	return &queryResult{columns: columns, rows: rows, nulls: nulls}
}

func nullLabel(attribute catalog.ColumnAttribute) string {
	if attribute.Nullable {
		return "YES"
	}
	return "NO"
}

func showColumnExtra(attribute catalog.ColumnAttribute) string {
	if attribute.AutoIncrement {
		return "auto_increment"
	}
	return ""
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
