package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jonbaldie/database/internal/lifecycle"
)

// configurationError is deliberately small because its class is part of the
// operator contract while its prose is diagnostic context for a human.
type configurationError struct {
	class   string
	message string
}

func (e *configurationError) Error() string { return e.message }

type configuration struct {
	options lifecycle.Options
	values  map[string]configurationValue
}

type configurationValue struct {
	value  string
	source string
}

type configurationSetting struct {
	defaultValue string
	flag         string
	minimum      int64
	maximum      int64
}

var configurationRegistry = map[string]configurationSetting{
	"data_directory":                          {defaultValue: "", flag: "--data-directory"},
	"mysql_listen_address":                    {defaultValue: "127.0.0.1:3306", flag: "--mysql-listen-address"},
	"tls_certificate_file":                    {defaultValue: "", flag: "--tls-certificate-file"},
	"tls_private_key_file":                    {defaultValue: "", flag: "--tls-private-key-file"},
	"diagnostics_listen_address":              {defaultValue: "", flag: "--diagnostics-listen-address"},
	"log_format":                              {defaultValue: "json", flag: "--log-format"},
	"statement_timeout_ms":                    numericConfigurationSetting("300000", "--statement-timeout-ms", 1, maximumInt64),
	"lock_wait_timeout_ms":                    numericConfigurationSetting("5000", "--lock-wait-timeout-ms", 1, maximumInt64),
	"idle_in_transaction_timeout_ms":          numericConfigurationSetting("300000", "--idle-in-transaction-timeout-ms", 1, maximumInt64),
	"idle_session_timeout_ms":                 numericConfigurationSetting("3600000", "--idle-session-timeout-ms", 1, maximumInt64),
	"execution_memory_limit_bytes":            numericConfigurationSetting("67108864", "--execution-memory-limit-bytes", 1, maximumInt64),
	"aggregate_execution_memory_limit_bytes":  numericConfigurationSetting("2147483648", "--aggregate-execution-memory-limit-bytes", 1, maximumInt64),
	"temporary_storage_limit_bytes":           numericConfigurationSetting("17179869184", "--temporary-storage-limit-bytes", 1, maximumInt64),
	"aggregate_temporary_storage_limit_bytes": numericConfigurationSetting("34359738368", "--aggregate-temporary-storage-limit-bytes", 1, maximumInt64),
	"max_connections":                         numericConfigurationSetting("100", "--max-connections", 1, 2147483647),
	"max_allowed_packet":                      numericConfigurationSetting("67108864", "--max-allowed-packet", 1024, 1073741824),
	"max_prepared_stmt_count":                 numericConfigurationSetting("4096", "--max-prepared-stmt-count", 1, 2147483647),
}

const maximumInt64 = int64(1<<63 - 1)

func numericConfigurationSetting(defaultValue, flag string, minimum, maximum int64) configurationSetting {
	return configurationSetting{defaultValue: defaultValue, flag: flag, minimum: minimum, maximum: maximum}
}

// parseServeFlags is retained as the compatibility seam used by the serve
// command and package tests. It now resolves all three public input sources.
func parseServeFlags(args []string) (lifecycle.Options, error) {
	config, err := resolveConfiguration(args, os.Environ())
	if err != nil {
		return lifecycle.Options{}, err
	}
	return config.options, nil
}

func resolveConfiguration(args, environment []string) (configuration, error) {
	inputs, err := parseConfigurationInputs(args, environment)
	if err != nil {
		return configuration{}, err
	}
	values, err := resolvedConfigurationValues(inputs)
	if err != nil {
		return configuration{}, err
	}
	config, err := makeConfiguration(values)
	if err != nil {
		return configuration{}, err
	}
	if config.options.DataDirectory == "" {
		return configuration{}, invalidConfiguration("data_directory is required")
	}
	return config, nil
}

type configurationInputs struct {
	flags, environment map[string]string
	path               string
}

func parseConfigurationInputs(args, environment []string) (configurationInputs, error) {
	path, flags, err := parseConfigurationFlags(args)
	if err != nil {
		return configurationInputs{}, err
	}
	values, environmentPath, err := parseConfigurationEnvironment(environment)
	if err != nil {
		return configurationInputs{}, err
	}
	if path == "" {
		path = environmentPath
	}
	return configurationInputs{flags: flags, environment: values, path: path}, nil
}

func resolvedConfigurationValues(inputs configurationInputs) (map[string]configurationValue, error) {
	values := defaultConfigurationValues()
	if inputs.path != "" {
		fileValues, err := readConfigurationFile(inputs.path)
		if err != nil {
			return nil, err
		}
		applyConfigurationValues(values, fileValues, "file:"+inputs.path)
	}
	applyConfigurationValues(values, inputs.environment, "environment")
	applyConfigurationValues(values, inputs.flags, "flag")
	return values, nil
}

func defaultConfigurationValues() map[string]configurationValue {
	values := make(map[string]configurationValue, len(configurationRegistry))
	for name, setting := range configurationRegistry {
		values[name] = configurationValue{value: setting.defaultValue, source: "default"}
	}
	return values
}

func applyConfigurationValues(destination map[string]configurationValue, source map[string]string, label string) {
	for name, value := range source {
		destination[name] = configurationValue{value: value, source: label}
	}
}

func parseConfigurationFlags(args []string) (string, map[string]string, error) {
	parser := configurationFlagParser{values: map[string]string{}, seen: map[string]bool{}}
	for index, count := 0, len(args); index < count; {
		next, err := parser.consume(args, index)
		if err != nil {
			return "", nil, err
		}
		index = next
	}
	return parser.path, parser.values, nil
}

type configurationFlagParser struct {
	values   map[string]string
	seen     map[string]bool
	path     string
	pathSeen bool
}

func (parser *configurationFlagParser) consume(args []string, index int) (int, error) {
	name, value, next, err := configurationFlagValue(args, index)
	if err != nil {
		return 0, err
	}
	if name == "--config" {
		return next, parser.setPath(value)
	}
	canonical, normalized, ok := flagSetting(name, value)
	if !ok {
		return 0, invalidConfiguration(fmt.Sprintf("unknown flag %q", name))
	}
	return next, parser.add(canonical, normalized)
}

func configurationFlagValue(args []string, index int) (string, string, int, error) {
	argument := args[index]
	name, value, hasValue := strings.Cut(argument, "=")
	if hasValue {
		return checkedConfigurationFlagValue(name, value, index+1)
	}
	if !knownConfigurationFlag(name) {
		return "", "", 0, invalidConfiguration(fmt.Sprintf("unknown flag %q", argument))
	}
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
		return "", "", 0, invalidConfiguration(name + " requires a non-empty value")
	}
	return checkedConfigurationFlagValue(name, args[index+1], index+2)
}

func checkedConfigurationFlagValue(name, value string, next int) (string, string, int, error) {
	if value == "" {
		return "", "", 0, invalidConfiguration(fmt.Sprintf("%s has an empty value", name))
	}
	return name, value, next, nil
}

func knownConfigurationFlag(name string) bool {
	return name == "--config" || configurationFlagNames[name] != ""
}

func (parser *configurationFlagParser) add(name, value string) error {
	if parser.seen[name] {
		return invalidConfiguration("repeated setting " + name)
	}
	parser.seen[name], parser.values[name] = true, value
	return nil
}

func (parser *configurationFlagParser) setPath(path string) error {
	if parser.pathSeen {
		return invalidConfiguration("repeated setting config")
	}
	parser.pathSeen, parser.path = true, path
	return nil
}

func flagSetting(name, value string) (string, string, bool) {
	canonical := configurationFlagNames[name]
	if canonical == "" {
		return "", "", false
	}
	return canonical, value, true
}

var configurationFlagNames = configurationFlags()

func configurationFlags() map[string]string {
	flags := make(map[string]string, len(configurationRegistry))
	for name, setting := range configurationRegistry {
		if setting.flag != "" {
			flags[setting.flag] = name
		}
	}
	return flags
}

func parseConfigurationEnvironment(environment []string) (map[string]string, string, error) {
	parser := configurationEnvironmentParser{values: map[string]string{}, seen: map[string]bool{}}
	for _, entry := range environment {
		if err := parser.add(entry); err != nil {
			return nil, "", err
		}
	}
	return parser.values, parser.path, nil
}

type configurationEnvironmentParser struct {
	values map[string]string
	seen   map[string]bool
	path   string
}

func (parser *configurationEnvironmentParser) add(entry string) error {
	name, value, belongs := configurationEnvironmentEntry(entry)
	if !belongs {
		return nil
	}
	if name == "DATABASE_SERVER_CONFIG" {
		return parser.addPath(name, value)
	}
	return parser.addValue(name, value)
}

func configurationEnvironmentEntry(entry string) (string, string, bool) {
	name, value, valid := strings.Cut(entry, "=")
	return name, value, valid && strings.HasPrefix(name, "DATABASE_SERVER_")
}

func (parser *configurationEnvironmentParser) addPath(name, value string) error {
	if parser.seen[name] {
		return invalidConfiguration("repeated setting config")
	}
	if strings.TrimSpace(value) == "" {
		return invalidConfiguration(name + " has an empty value")
	}
	parser.seen[name], parser.path = true, value
	return nil
}

func (parser *configurationEnvironmentParser) addValue(name, value string) error {
	canonical, err := canonicalEnvironmentName(name)
	if err != nil {
		return err
	}
	if parser.seen[name] {
		return invalidConfiguration("repeated environment setting " + canonical)
	}
	if strings.TrimSpace(value) == "" {
		return invalidConfiguration(name + " has an empty value")
	}
	parser.seen[name], parser.values[canonical] = true, value
	return nil
}

func canonicalEnvironmentName(name string) (string, error) {
	suffix := strings.TrimPrefix(name, "DATABASE_SERVER_")
	if suffix != strings.ToUpper(suffix) {
		return "", invalidConfiguration(fmt.Sprintf("unknown environment setting %q", name))
	}
	canonical := strings.ToLower(suffix)
	if !isConfigurationSetting(canonical) {
		return "", invalidConfiguration(fmt.Sprintf("unknown environment setting %q", name))
	}
	return canonical, nil
}

func readConfigurationFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, &configurationError{class: "precondition", message: fmt.Sprintf("read config file: %v", err)}
	}
	defer file.Close()
	parser := configurationFileParser{values: map[string]string{}, seen: map[string]bool{}}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := parser.add(scanner.Text()); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, &configurationError{class: "precondition", message: fmt.Sprintf("read config file: %v", err)}
	}
	return parser.values, nil
}

type configurationFileParser struct {
	values     map[string]string
	seen       map[string]bool
	lineNumber int
}

func (parser *configurationFileParser) add(raw string) error {
	parser.lineNumber++
	line := strings.TrimSpace(stripTOMLComment(raw))
	if line == "" {
		return nil
	}
	key, value, err := parseConfigurationFileLine(line, parser.lineNumber)
	if err != nil {
		return err
	}
	if parser.seen[key] {
		return invalidConfiguration("duplicate config setting " + key)
	}
	parser.seen[key], parser.values[key] = true, value
	return nil
}

func parseConfigurationFileLine(line string, lineNumber int) (string, string, error) {
	key, raw, err := splitConfigurationLine(line, lineNumber)
	if err != nil {
		return "", "", err
	}
	if !isConfigurationSetting(key) {
		return "", "", invalidConfiguration(fmt.Sprintf("config line %d: unknown setting %q", lineNumber, key))
	}
	value, err := parseConfigurationValue(key, raw, lineNumber)
	if err != nil {
		return "", "", err
	}
	return key, value, nil
}

func splitConfigurationLine(line string, lineNumber int) (string, string, error) {
	if strings.HasPrefix(line, "[") {
		return "", "", invalidConfiguration(fmt.Sprintf("config line %d: tables are not supported", lineNumber))
	}
	key, raw, valid := strings.Cut(line, "=")
	if !valid {
		return "", "", invalidConfiguration(fmt.Sprintf("config line %d: expected one key=value pair", lineNumber))
	}
	return strings.TrimSpace(key), raw, nil
}

func parseConfigurationValue(key, raw string, lineNumber int) (string, error) {
	setting := configurationRegistry[key]
	value, quoted, err := tomlValue(strings.TrimSpace(raw))
	if err != nil {
		return "", invalidConfiguration(fmt.Sprintf("config line %d: %s", lineNumber, err))
	}
	if !configurationValueFormAccepted(setting, quoted) {
		return "", invalidConfiguration(fmt.Sprintf("config line %d: %s has the wrong TOML value form", lineNumber, key))
	}
	if value == "" {
		return "", invalidConfiguration("config setting " + key + " has an empty value")
	}
	return value, nil
}

func configurationValueFormAccepted(setting configurationSetting, quoted bool) bool {
	return (setting.minimum == 0) == quoted
}

func isConfigurationSetting(name string) bool {
	_, exists := configurationRegistry[name]
	return exists
}

func stripTOMLComment(line string) string {
	state := tomlCommentState{}
	for index, count := 0, len(line); index < count; index++ {
		if state.isComment(line[index]) {
			return line[:index]
		}
	}
	return line
}

type tomlCommentState struct {
	quote   byte
	escaped bool
}

func (state *tomlCommentState) isComment(character byte) bool {
	if state.quote != 0 {
		state.consumeQuoted(character)
		return false
	}
	return state.consumeUnquoted(character)
}

func (state *tomlCommentState) consumeQuoted(character byte) {
	if state.quote == '"' && state.escaped {
		state.escaped = false
		return
	}
	if state.quote == '"' && character == '\\' {
		state.escaped = true
		return
	}
	if character == state.quote {
		state.quote = 0
	}
}

func (state *tomlCommentState) consumeUnquoted(character byte) bool {
	if character == '"' || character == '\'' {
		state.quote = character
		return false
	}
	return character == '#'
}

func tomlValue(raw string) (string, bool, error) {
	if isQuotedTOMLValue(raw) {
		value, err := quotedTOMLValue(raw)
		return value, true, err
	}
	if !isBareDecimalTOMLValue(raw) {
		return "", false, errors.New("invalid TOML value")
	}
	return raw, false, nil
}

func isQuotedTOMLValue(raw string) bool {
	if len(raw) < 2 {
		return false
	}
	return raw[0] == '"' && raw[len(raw)-1] == '"' || raw[0] == '\'' && raw[len(raw)-1] == '\''
}

func quotedTOMLValue(raw string) (string, error) {
	if err := validateTOMLStringCharacters(raw); err != nil {
		return "", err
	}
	if raw[0] == '\'' {
		if strings.Contains(raw[1:len(raw)-1], "'") {
			return "", errors.New("invalid TOML literal string")
		}
		return raw[1 : len(raw)-1], nil
	}
	if err := validateTOMLBasicString(raw); err != nil {
		return "", err
	}
	value, err := strconv.Unquote(raw)
	if err != nil {
		return "", errors.New("invalid quoted value")
	}
	return value, nil
}

func isBareDecimalTOMLValue(raw string) bool {
	return raw != "" && strings.Trim(raw, "0123456789") == "" && (len(raw) == 1 || raw[0] != '0')
}

func validateTOMLStringCharacters(raw string) error {
	if !utf8.ValidString(raw[1 : len(raw)-1]) {
		return errors.New("invalid TOML string UTF-8")
	}
	for _, character := range raw[1 : len(raw)-1] {
		if character <= 0x1f || character == 0x7f {
			return errors.New("invalid TOML string character")
		}
	}
	return nil
}

func validateTOMLBasicString(raw string) error {
	limit := len(raw) - 1
	for index := 1; index < limit; index++ {
		if raw[index] != '\\' {
			continue
		}
		index++
		if index >= limit {
			return errors.New("invalid TOML basic string escape")
		}
		switch raw[index] {
		case '"', '\\', 'b', 't', 'n', 'f', 'r':
		case 'u':
			if !tomlHexEscape(raw, index+1, 4) {
				return errors.New("invalid TOML basic string escape")
			}
			index += 4
		case 'U':
			if !tomlHexEscape(raw, index+1, 8) {
				return errors.New("invalid TOML basic string escape")
			}
			index += 8
		default:
			return errors.New("invalid TOML basic string escape")
		}
	}
	return nil
}

func tomlHexEscape(raw string, start, length int) bool {
	if start+length > len(raw)-1 {
		return false
	}
	for _, character := range raw[start : start+length] {
		if !('0' <= character && character <= '9') && !('a' <= character && character <= 'f') && !('A' <= character && character <= 'F') {
			return false
		}
	}
	return true
}

func makeConfiguration(values map[string]configurationValue) (configuration, error) {
	parts, err := buildConfigurationParts(configurationValueLookup(values))
	if err != nil {
		return configuration{}, err
	}
	return configuration{options: parts.options(), values: values}, nil
}

func configurationValueLookup(values map[string]configurationValue) func(string) string {
	return func(name string) string { return values[name].value }
}

type configurationParts struct {
	dataDirectory, mysqlAddress, diagnosticsAddress, certificate, key, format string
	limits                                                                    configurationLimits
}

func buildConfigurationParts(get func(string) string) (configurationParts, error) {
	paths, err := configurationPaths(get)
	if err != nil {
		return configurationParts{}, err
	}
	addresses, err := configurationAddresses(get)
	if err != nil {
		return configurationParts{}, err
	}
	if err := validateTLS(paths.certificate, paths.key); err != nil {
		return configurationParts{}, err
	}
	format, err := configurationFormat(get("log_format"))
	if err != nil {
		return configurationParts{}, err
	}
	limits, err := configurationNumbers(get)
	if err != nil {
		return configurationParts{}, err
	}
	parts := configurationParts{dataDirectory: paths.dataDirectory, certificate: paths.certificate, key: paths.key, mysqlAddress: addresses.mysql, diagnosticsAddress: addresses.diagnostics, format: format, limits: limits}
	return parts, validateConfigurationParts(parts)
}

type configurationPathsResult struct{ dataDirectory, certificate, key string }

func configurationPaths(get func(string) string) (configurationPathsResult, error) {
	dataDirectory, err := absolutePath("data_directory", get("data_directory"), false)
	if err != nil {
		return configurationPathsResult{}, err
	}
	certificate, err := absolutePath("tls_certificate_file", get("tls_certificate_file"), true)
	if err != nil {
		return configurationPathsResult{}, err
	}
	key, err := absolutePath("tls_private_key_file", get("tls_private_key_file"), true)
	return configurationPathsResult{dataDirectory, certificate, key}, err
}

type configurationAddressesResult struct{ mysql, diagnostics string }

func configurationAddresses(get func(string) string) (configurationAddressesResult, error) {
	mysql, err := networkAddress("mysql_listen_address", get("mysql_listen_address"))
	if err != nil {
		return configurationAddressesResult{}, err
	}
	diagnostics := ""
	if get("diagnostics_listen_address") != "" {
		diagnostics, err = networkAddress("diagnostics_listen_address", get("diagnostics_listen_address"))
	}
	return configurationAddressesResult{mysql, diagnostics}, err
}

func validateTLS(certificate, key string) error {
	if (certificate == "") != (key == "") {
		return invalidConfiguration("tls_certificate_file and tls_private_key_file must be provided together")
	}
	if certificate == "" {
		return nil
	}
	if _, err := tls.LoadX509KeyPair(certificate, key); err != nil {
		return invalidConfiguration(fmt.Sprintf("invalid TLS certificate/key pair: %v", err))
	}
	return nil
}

func configurationFormat(format string) (string, error) {
	if format != "json" && format != "text" {
		return "", invalidConfiguration("log_format must be json or text")
	}
	return format, nil
}

func validateConfigurationParts(parts configurationParts) error {
	if parts.limits.executionMemory > parts.limits.aggregateMemory {
		return invalidConfiguration("execution_memory_limit_bytes cannot exceed aggregate_execution_memory_limit_bytes")
	}
	if parts.limits.temporaryStorage > parts.limits.aggregateTemporaryStorage {
		return invalidConfiguration("temporary_storage_limit_bytes cannot exceed aggregate_temporary_storage_limit_bytes")
	}
	if parts.diagnosticsAddress != "" && parts.diagnosticsAddress == parts.mysqlAddress {
		return invalidConfiguration("MySQL and diagnostics listeners must use different addresses")
	}
	return nil
}

func (parts configurationParts) options() lifecycle.Options {
	format := parts.format
	if format == "text" {
		format = "human"
	}
	return lifecycle.Options{
		DataDirectory: parts.dataDirectory, MySQLAddress: parts.mysqlAddress, TLSCertFile: parts.certificate,
		TLSKeyFile: parts.key, DiagnosticsAddress: parts.diagnosticsAddress, Format: format, MySQLEnabled: true,
		Timeouts: lifecycle.Timeouts{
			StatementTimeoutMilliseconds: parts.limits.statementTimeout, LockWaitTimeoutMilliseconds: parts.limits.lockWaitTimeout,
			IdleInTransactionTimeoutMilliseconds: parts.limits.idleTransactionTimeout, IdleSessionTimeoutMilliseconds: parts.limits.idleSessionTimeout,
		},
		ResourceLimits: lifecycle.ResourceLimits{
			ExecutionMemoryLimitBytes: parts.limits.executionMemory, AggregateMemoryLimitBytes: parts.limits.aggregateMemory,
			TemporaryStorageLimitBytes: parts.limits.temporaryStorage, AggregateTemporaryLimitBytes: parts.limits.aggregateTemporaryStorage,
		},
		ConnectionLimits: lifecycle.ConnectionLimits{
			MaxConnections: int(parts.limits.maxConnections), MaxAllowedPacket: parts.limits.maxAllowedPacket,
			MaxPreparedStmtCount: int(parts.limits.maxPreparedStatements),
		},
	}
}

type configurationLimits struct {
	statementTimeout, lockWaitTimeout, idleTransactionTimeout, idleSessionTimeout int64
	executionMemory, aggregateMemory, temporaryStorage, aggregateTemporaryStorage int64
	maxConnections, maxAllowedPacket, maxPreparedStatements                       int64
}

func configurationNumbers(get func(string) string) (configurationLimits, error) {
	values := make(map[string]int64, len(configurationRegistry))
	for name, setting := range configurationRegistry {
		if setting.minimum == 0 {
			continue
		}
		value, err := positiveInteger(name, get(name), setting.minimum, setting.maximum)
		if err != nil {
			return configurationLimits{}, err
		}
		values[name] = value
	}
	return configurationLimits{statementTimeout: values["statement_timeout_ms"], lockWaitTimeout: values["lock_wait_timeout_ms"], idleTransactionTimeout: values["idle_in_transaction_timeout_ms"], idleSessionTimeout: values["idle_session_timeout_ms"], executionMemory: values["execution_memory_limit_bytes"], aggregateMemory: values["aggregate_execution_memory_limit_bytes"], temporaryStorage: values["temporary_storage_limit_bytes"], aggregateTemporaryStorage: values["aggregate_temporary_storage_limit_bytes"], maxConnections: values["max_connections"], maxAllowedPacket: values["max_allowed_packet"], maxPreparedStatements: values["max_prepared_stmt_count"]}, nil
}

func absolutePath(name, value string, optional bool) (string, error) {
	if value == "" {
		if optional {
			return "", nil
		}
		return "", nil
	}
	if !filepath.IsAbs(value) {
		return "", invalidConfiguration(name + " must be an absolute path")
	}
	return filepath.Clean(value), nil
}

func networkAddress(name, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", invalidConfiguration(name + " has an empty value")
	}
	host, port, err := net.SplitHostPort(value)
	ip := net.ParseIP(host)
	if err != nil || host == "" || ip == nil {
		return "", invalidConfiguration(name + " must be an IPv4 address or bracketed IPv6 address with a port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", invalidConfiguration(name + " port must be between 1 and 65535")
	}
	canonicalHost := ip.String()
	if strings.Contains(canonicalHost, ":") {
		return "[" + canonicalHost + "]:" + strconv.Itoa(portNumber), nil
	}
	return canonicalHost + ":" + strconv.Itoa(portNumber), nil
}

func positiveInteger(name, value string, minimum, maximum int64) (int64, error) {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, invalidConfiguration(name + " must be a positive base-10 integer")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, invalidConfiguration(fmt.Sprintf("%s must be between %d and %d", name, minimum, maximum))
	}
	return parsed, nil
}

func invalidConfiguration(message string) error {
	return &configurationError{class: "invalid_input", message: message}
}

func configurationResult(config configuration, operationID string) map[string]any {
	settings := make(map[string]any, len(config.values))
	for name, setting := range config.values {
		value := setting.value
		if name == "tls_private_key_file" && value != "" {
			value = "[redacted]"
		}
		settings[name] = map[string]string{"value": value, "source": setting.source}
	}
	return map[string]any{"schema": "database.configuration/v1", "operation_id": operationID, "settings": settings}
}

func configurationClass(err error) string {
	var configErr *configurationError
	if errors.As(err, &configErr) {
		return configErr.class
	}
	return "operation_failed"
}

func writeConfigurationDocumentation(w io.Writer) {
	fmt.Fprintln(w, "Configuration sources (highest precedence first): flags, DATABASE_SERVER_ environment variables, explicit TOML file, defaults.")
	fmt.Fprintln(w, "The TOML file is selected only by --config PATH or DATABASE_SERVER_CONFIG.")
}
