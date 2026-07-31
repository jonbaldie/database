package mysql

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const fixedSQLMode = "STRICT_ALL_TABLES,ONLY_FULL_GROUP_BY,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO"

// resourceLimits returns the effective per-statement limits for this session.
func (s *session) resourceLimits() ResourceLimits {
	return ResourceLimits{
		StatementTimeout:           s.settings.statementTimeout,
		ExecutionMemoryLimitBytes:  s.settings.executionMemoryLimit,
		TemporaryStorageLimitBytes: s.settings.temporaryStorageLimit,
	}
}

func (s *session) resetSessionSettings() {
	s.settings.collationConnection = collation0900AICI
	s.settings.statementTimeout = s.server.config.ResourceLimits.StatementTimeout
	s.settings.lockWaitTimeout = s.server.config.LockWaitTimeout
	s.settings.executionMemoryLimit = s.server.config.ResourceLimits.ExecutionMemoryLimitBytes
	s.settings.temporaryStorageLimit = s.server.config.ResourceLimits.TemporaryStorageLimitBytes
}

func (s *textStatementExecutor) planningSettings() map[string]string {
	isolation, _ := s.session.sessionVariable("transaction_isolation")
	statementTimeout, _ := s.session.sessionVariable("statement_timeout_ms")
	memory, _ := s.session.sessionVariable("execution_memory_limit_bytes")
	temporary, _ := s.session.sessionVariable("temporary_storage_limit_bytes")
	return map[string]string{
		"sql_mode": fixedSQLMode, "transaction_isolation": isolation,
		"statement_timeout_ms": statementTimeout, "execution_memory_limit_bytes": memory,
		"temporary_storage_limit_bytes": temporary,
	}
}

func canonicalCollation(kind collationKind) string {
	if kind == collationBin {
		return "utf8mb4_bin"
	}
	return "utf8mb4_0900_ai_ci"
}

func sessionVariableName(name string) (scope, canonical string) {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "@@")
	for _, prefix := range []string{"session.", "session ", "global.", "global "} {
		if strings.HasPrefix(name, prefix) {
			scope = strings.TrimSuffix(strings.TrimSpace(prefix), ".")
			name = strings.TrimSpace(strings.TrimPrefix(name, prefix))
			break
		}
	}
	return scope, strings.Trim(name, "`")
}

type sessionVariableReader func(*session) (string, error)

var sessionVariableReaders = map[string]sessionVariableReader{
	"autocommit": sessionAutocommit, "transaction_isolation": sessionIsolation, "transaction_read_only": sessionReadOnly,
	"tx_isolation": sessionIsolation, "auto_increment_increment": fixedSessionVariable("1"),
	"time_zone": sessionTimeZone, "collation_connection": sessionCollation, "statement_timeout_ms": sessionStatementTimeout,
	"lock_wait_timeout_ms": sessionLockWaitTimeout, "execution_memory_limit_bytes": sessionMemoryLimit,
	"temporary_storage_limit_bytes": sessionTemporaryLimit, "sql_mode": fixedSessionVariable(fixedSQLMode),
	"character_set_client": fixedSessionVariable("utf8mb4"), "character_set_connection": fixedSessionVariable("utf8mb4"),
	"character_set_results": fixedSessionVariable("utf8mb4"), "character_set_server": fixedSessionVariable("utf8mb4"),
	"collation_server": fixedSessionVariable("utf8mb4_0900_ai_ci"), "system_time_zone": fixedSessionVariable("+00:00"),
	"init_connect": fixedSessionVariable(""), "interactive_timeout": fixedSessionVariable("28800"),
	"language": fixedSessionVariable("English"), "lower_case_table_names": fixedSessionVariable("0"),
	"net_write_timeout": fixedSessionVariable("60"), "performance_schema": fixedSessionVariable("0"),
	"wait_timeout":       fixedSessionVariable("28800"),
	"max_allowed_packet": sessionMaxAllowedPacket, "max_connections": sessionMaxConnections,
	"max_prepared_stmt_count": sessionMaxPreparedStatements, "idle_session_timeout_ms": fixedSessionVariable("0"),
	"idle_in_transaction_timeout_ms": fixedSessionVariable("0"), "aggregate_execution_memory_limit_bytes": sessionAggregateMemory,
	"aggregate_temporary_storage_limit_bytes": sessionAggregateTemporary, "version": sessionVersion,
	"version_comment": fixedSessionVariable("database"), "license": fixedSessionVariable("database license"),
	"protocol_version": fixedSessionVariable("10"),
}

var readOnlySessionVariables = map[string]bool{
	"sql_mode": true, "character_set_client": true, "character_set_connection": true, "character_set_results": true,
	"max_allowed_packet": true, "max_connections": true, "max_prepared_stmt_count": true, "idle_session_timeout_ms": true,
	"idle_in_transaction_timeout_ms": true, "aggregate_execution_memory_limit_bytes": true,
	"aggregate_temporary_storage_limit_bytes": true, "character_set_server": true, "collation_server": true,
	"system_time_zone": true, "version": true, "version_comment": true, "license": true, "protocol_version": true,
	"tx_isolation": true, "auto_increment_increment": true, "init_connect": true, "interactive_timeout": true,
	"language": true, "lower_case_table_names": true, "net_write_timeout": true, "performance_schema": true,
	"wait_timeout": true,
}

func knownSessionVariable(name string) bool { _, found := sessionVariableReaders[name]; return found }

func fixedSessionVariable(value string) sessionVariableReader {
	return func(*session) (string, error) { return value, nil }
}

func sessionAutocommit(s *session) (string, error) {
	if s.autocommitOff {
		return "OFF", nil
	}
	return "ON", nil
}
func sessionIsolation(s *session) (string, error) {
	if s.isolation == isolationReadCommitted {
		return "READ-COMMITTED", nil
	}
	return "REPEATABLE-READ", nil
}
func sessionReadOnly(s *session) (string, error) {
	if s.readOnly {
		return "ON", nil
	}
	return "OFF", nil
}
func sessionTimeZone(s *session) (string, error) {
	offset, err := sessionTimeZoneOffset(s)
	return formatFixedOffset(offset), err
}
func sessionCollation(s *session) (string, error) {
	return canonicalCollation(s.settings.collationConnection), nil
}
func sessionStatementTimeout(s *session) (string, error) {
	return strconv.FormatInt(s.settings.statementTimeout.Milliseconds(), 10), nil
}
func sessionLockWaitTimeout(s *session) (string, error) {
	return strconv.FormatInt(s.settings.lockWaitTimeout.Milliseconds(), 10), nil
}
func sessionMemoryLimit(s *session) (string, error) {
	return strconv.FormatInt(s.settings.executionMemoryLimit, 10), nil
}
func sessionTemporaryLimit(s *session) (string, error) {
	return strconv.FormatInt(s.settings.temporaryStorageLimit, 10), nil
}
func sessionMaxAllowedPacket(s *session) (string, error) {
	return strconv.FormatInt(s.server.config.MaxAllowedPacket, 10), nil
}
func sessionMaxConnections(s *session) (string, error) {
	return strconv.Itoa(s.server.config.MaxConnections), nil
}
func sessionMaxPreparedStatements(s *session) (string, error) {
	return strconv.Itoa(s.server.config.MaxPreparedStmtCount), nil
}
func sessionAggregateMemory(s *session) (string, error) {
	return strconv.FormatInt(s.server.config.ResourceLimits.AggregateExecutionMemoryLimitBytes, 10), nil
}
func sessionAggregateTemporary(s *session) (string, error) {
	return strconv.FormatInt(s.server.config.ResourceLimits.AggregateTemporaryStorageLimitBytes, 10), nil
}
func sessionVersion(s *session) (string, error) {
	return "8.4.11-database-" + s.server.config.Version, nil
}

func (s *session) sessionVariable(name string) (string, error) {
	scope, name := sessionVariableName(name)
	if scope == "global" {
		defaults := *s
		defaults.autocommitOff = false
		defaults.isolation, defaults.nextIsolation = isolationRepeatableRead, isolationRepeatableRead
		defaults.readOnly, defaults.nextReadOnly = false, false
		defaults.timeZone = defaults.initialTimeZone
		defaults.resetSessionSettings()
		return defaults.sessionVariable(name)
	}
	reader, found := sessionVariableReaders[name]
	if !found {
		return "", sqlFailure{1193, "HY000", "unknown system variable '" + name + "'"}
	}
	return reader(s)
}

// sessionVariableExpression recognises the exact @@name form used by client
// startup probes. The general scalar expression grammar deliberately does not
// include system-variable tokens, so resolve this small compatibility seam
// before handing other expressions to that grammar.
func sessionVariableExpression(s *session, expression string) (exprValue, bool, error) {
	trimmed := strings.TrimSpace(expression)
	if !strings.HasPrefix(trimmed, "@@") || strings.ContainsAny(trimmed[2:], " \t\r\n+-*/%<>=(),") {
		return exprValue{}, false, nil
	}
	value, err := s.sessionVariable(trimmed)
	if err != nil {
		return exprValue{}, true, err
	}
	return stringValue(value), true, nil
}

func sessionVariableMetadata(s *session, expression string) (columnMetadata, bool, error) {
	value, handled, err := sessionVariableExpression(s, expression)
	if !handled {
		return columnMetadata{}, false, nil
	}
	if err != nil {
		return columnMetadata{}, true, err
	}
	return scalarMetadata(strings.TrimSpace(expression), value.render(), value), true, nil
}

func (s *textStatementExecutor) sessionVariableResult(name string) (*queryResult, error) {
	value, err := s.session.sessionVariable(name)
	if err != nil {
		return nil, err
	}
	_, canonical := sessionVariableName(name)
	return &queryResult{columns: []string{"@@" + canonical}, rows: [][]string{{value}}}, nil
}

func (s *textStatementExecutor) applySessionAssignments(query string) error {
	rest := strings.TrimSpace(query[len("SET "):])
	lower := strings.ToLower(rest)
	if strings.HasPrefix(lower, "global ") || strings.HasPrefix(lower, "@@global.") {
		return sqlFailure{1231, "42000", "SET GLOBAL is unsupported"}
	}
	if strings.HasPrefix(lower, "names ") {
		return s.applySetNames(rest[len("names "):])
	}
	for _, assignment := range splitCSV(rest) {
		lhs, rawValue, found := strings.Cut(assignment, "=")
		if !found {
			return sqlFailure{1064, "42000", "malformed session setting"}
		}
		if err := s.applySessionVariable(strings.TrimSpace(lhs), strings.TrimSpace(rawValue)); err != nil {
			return err
		}
	}
	return nil
}

func (s *textStatementExecutor) applySetNames(value string) error {
	fields := strings.Fields(value)
	if len(fields) == 0 || !strings.EqualFold(scalar(fields[0]), "utf8mb4") {
		return sqlFailure{1231, "42000", "unsupported character set"}
	}
	s.session.settings.collationConnection = collation0900AICI
	if len(fields) == 1 {
		return nil
	}
	if len(fields) != 3 || !strings.EqualFold(fields[1], "collate") {
		return sqlFailure{1064, "42000", "malformed SET NAMES"}
	}
	return s.applySessionVariable("collation_connection", fields[2])
}

func (s *textStatementExecutor) applySessionVariable(rawName, rawValue string) error {
	scope, name := sessionVariableName(rawName)
	if scope == "global" {
		return sqlFailure{1231, "42000", "SET GLOBAL is unsupported"}
	}
	if !knownSessionVariable(name) {
		return sqlFailure{1193, "HY000", "unknown system variable '" + name + "'"}
	}
	if readOnlySessionVariables[name] {
		if !readOnlySessionAssignmentAllowed(name, scalar(rawValue)) {
			return sqlFailure{1238, "HY000", "variable is read only"}
		}
		return nil
	}
	if strings.EqualFold(rawValue, "default") {
		return s.resetSessionVariable(name)
	}
	writer := sessionVariableWriters[name]
	return writer(s, scalar(rawValue))
}

func readOnlySessionAssignmentAllowed(name, value string) bool {
	if name == "sql_mode" {
		return strings.EqualFold(value, fixedSQLMode) || strings.Contains(strings.ToUpper(value), "STRICT_TRANS_TABLES")
	}
	if strings.HasPrefix(name, "character_set_") {
		return strings.EqualFold(value, "utf8mb4") || (name == "character_set_results" && strings.EqualFold(value, "null"))
	}
	return false
}

type sessionVariableWriter func(*textStatementExecutor, string) error

var sessionVariableWriters = map[string]sessionVariableWriter{
	"autocommit": applyAutocommitValue, "transaction_isolation": applyIsolationValue,
	"transaction_read_only": applyReadOnlyValue, "time_zone": applyTimeZoneValue,
	"collation_connection": applyCollationValue, "statement_timeout_ms": applyStatementTimeoutValue,
	"lock_wait_timeout_ms": applyLockWaitTimeoutValue, "execution_memory_limit_bytes": applyMemoryLimitValue,
	"temporary_storage_limit_bytes": applyTemporaryLimitValue,
}

func applyAutocommitValue(s *textStatementExecutor, value string) error {
	off, found := map[string]bool{"0": true, "off": true, "false": true, "1": false, "on": false, "true": false}[strings.ToLower(value)]
	if !found {
		return sqlFailure{1231, "42000", "autocommit has an invalid value"}
	}
	if !off && s.session.transaction {
		if err := (&transactionExecutor{s.session}).commit(); err != nil {
			return err
		}
	}
	s.session.autocommitOff = off
	return nil
}

func applyIsolationValue(s *textStatementExecutor, value string) error {
	if err := sessionCharacteristicChangeAllowed(s.session); err != nil {
		return err
	}
	level, err := parseIsolationLevel(strings.ReplaceAll(value, "-", " "))
	if err != nil {
		return err
	}
	s.session.isolation, s.session.nextIsolation = level, level
	return nil
}

func applyReadOnlyValue(s *textStatementExecutor, value string) error {
	if err := sessionCharacteristicChangeAllowed(s.session); err != nil {
		return err
	}
	readOnly, found := map[string]bool{"1": true, "on": true, "true": true, "0": false, "off": false, "false": false}[strings.ToLower(value)]
	if !found {
		return sqlFailure{1231, "42000", "transaction_read_only has an invalid value"}
	}
	s.session.readOnly, s.session.nextReadOnly = readOnly, readOnly
	return nil
}

func sessionCharacteristicChangeAllowed(s *session) error {
	if s.transaction {
		return sqlFailure{1568, "25001", "transaction characteristics cannot change in an active transaction"}
	}
	return nil
}

func applyTimeZoneValue(s *textStatementExecutor, value string) error {
	offset, err := parseFixedOffset(value)
	if err == nil {
		s.session.timeZone = formatFixedOffset(offset)
	}
	return err
}

func applyCollationValue(s *textStatementExecutor, value string) error {
	kind, err := resolveCollation(characterText, value)
	if err == nil {
		s.session.settings.collationConnection = kind
	}
	return err
}

func applyStatementTimeoutValue(s *textStatementExecutor, value string) error {
	return s.setDurationLimit("statement_timeout_ms", value, s.session.server.config.ResourceLimits.StatementTimeout, &s.session.settings.statementTimeout)
}
func applyLockWaitTimeoutValue(s *textStatementExecutor, value string) error {
	return s.setDurationLimit("lock_wait_timeout_ms", value, s.session.server.config.LockWaitTimeout, &s.session.settings.lockWaitTimeout)
}
func applyMemoryLimitValue(s *textStatementExecutor, value string) error {
	return s.setByteLimit("execution_memory_limit_bytes", value, s.session.server.config.ResourceLimits.ExecutionMemoryLimitBytes, &s.session.settings.executionMemoryLimit)
}
func applyTemporaryLimitValue(s *textStatementExecutor, value string) error {
	return s.setByteLimit("temporary_storage_limit_bytes", value, s.session.server.config.ResourceLimits.TemporaryStorageLimitBytes, &s.session.settings.temporaryStorageLimit)
}

func (s *textStatementExecutor) resetSessionVariable(name string) error {
	sessionVariableResetters[name](s.session)
	return nil
}

var sessionVariableResetters = map[string]func(*session){
	"autocommit":            func(s *session) { s.autocommitOff = false },
	"transaction_isolation": func(s *session) { s.isolation, s.nextIsolation = isolationRepeatableRead, isolationRepeatableRead },
	"transaction_read_only": func(s *session) { s.readOnly, s.nextReadOnly = false, false },
	"time_zone":             func(s *session) { s.timeZone = s.initialTimeZone },
	"collation_connection":  func(s *session) { s.settings.collationConnection = collation0900AICI },
	"statement_timeout_ms":  func(s *session) { s.settings.statementTimeout = s.server.config.ResourceLimits.StatementTimeout },
	"lock_wait_timeout_ms":  func(s *session) { s.settings.lockWaitTimeout = s.server.config.LockWaitTimeout },
	"execution_memory_limit_bytes": func(s *session) {
		s.settings.executionMemoryLimit = s.server.config.ResourceLimits.ExecutionMemoryLimitBytes
	},
	"temporary_storage_limit_bytes": func(s *session) {
		s.settings.temporaryStorageLimit = s.server.config.ResourceLimits.TemporaryStorageLimitBytes
	},
}

func (s *textStatementExecutor) setDurationLimit(name, value string, ceiling time.Duration, target *time.Duration) error {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || time.Duration(parsed) > ceiling/time.Millisecond {
		return sqlFailure{1231, "42000", fmt.Sprintf("%s has an invalid value", name)}
	}
	*target = time.Duration(parsed) * time.Millisecond
	return nil
}

func (s *textStatementExecutor) setByteLimit(name, value string, ceiling int64, target *int64) error {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || parsed > ceiling {
		return sqlFailure{1231, "42000", fmt.Sprintf("%s has an invalid value", name)}
	}
	*target = parsed
	return nil
}

func (s *textStatementExecutor) showVariables(query string) (*queryResult, bool, error) {
	lower := strings.ToLower(strings.TrimSpace(query))
	if !showVariablesStatement(lower) {
		return nil, false, nil
	}
	like := showVariableLike(query, lower)
	result := &queryResult{columns: []string{"Variable_name", "Value"}}
	scope := showVariableScope(lower)
	for _, name := range publishedSessionVariables {
		if like != "" && !mysqlLike(name, like) {
			continue
		}
		value, err := s.session.sessionVariable(scope + name)
		if err != nil {
			return nil, true, err
		}
		result.rows = append(result.rows, []string{name, value})
	}
	return result, true, nil
}

var publishedSessionVariables = []string{"autocommit", "transaction_isolation", "transaction_read_only", "tx_isolation", "time_zone", "collation_connection", "statement_timeout_ms", "lock_wait_timeout_ms", "execution_memory_limit_bytes", "temporary_storage_limit_bytes", "sql_mode", "character_set_client", "character_set_connection", "character_set_results", "character_set_server", "collation_server", "system_time_zone", "auto_increment_increment", "init_connect", "interactive_timeout", "language", "lower_case_table_names", "max_allowed_packet", "max_connections", "max_prepared_stmt_count", "net_write_timeout", "performance_schema", "wait_timeout", "idle_session_timeout_ms", "idle_in_transaction_timeout_ms", "aggregate_execution_memory_limit_bytes", "aggregate_temporary_storage_limit_bytes", "version", "version_comment", "license", "protocol_version"}

func showVariablesStatement(lower string) bool {
	return strings.HasPrefix(lower, "show variables") || strings.HasPrefix(lower, "show session variables") || strings.HasPrefix(lower, "show global variables")
}

func showVariableScope(lower string) string {
	if strings.HasPrefix(lower, "show global variables") {
		return "global."
	}
	return ""
}

func showVariableLike(query, lower string) string {
	index := strings.Index(lower, " like ")
	if index < 0 {
		return ""
	}
	return strings.ToLower(scalar(strings.TrimSpace(query[index+len(" like "):])))
}

func mysqlLike(value, pattern string) bool {
	value, pattern = strings.ToLower(value), strings.ToLower(pattern)
	if !strings.Contains(pattern, "%") {
		return value == pattern
	}
	parts := strings.Split(pattern, "%")
	if !strings.HasPrefix(pattern, "%") && !strings.HasPrefix(value, parts[0]) {
		return false
	}
	position := 0
	for _, part := range parts {
		if part == "" {
			continue
		}
		index := strings.Index(value[position:], part)
		if index < 0 {
			return false
		}
		position += index + len(part)
	}
	return strings.HasSuffix(pattern, "%") || position == len(value)
}
