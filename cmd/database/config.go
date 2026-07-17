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

var configurationDefaults = map[string]string{
	"data_directory":                          "",
	"mysql_listen_address":                    "127.0.0.1:3306",
	"tls_certificate_file":                    "",
	"tls_private_key_file":                    "",
	"diagnostics_listen_address":              "",
	"log_format":                              "json",
	"statement_timeout_ms":                    "300000",
	"lock_wait_timeout_ms":                    "5000",
	"idle_in_transaction_timeout_ms":          "300000",
	"idle_session_timeout_ms":                 "3600000",
	"execution_memory_limit_bytes":            "67108864",
	"aggregate_execution_memory_limit_bytes":  "2147483648",
	"temporary_storage_limit_bytes":           "17179869184",
	"aggregate_temporary_storage_limit_bytes": "34359738368",
	"max_connections":                         "100",
	"max_allowed_packet":                      "67108864",
	"max_prepared_stmt_count":                 "4096",
}

var configurationNames = func() map[string]bool {
	result := make(map[string]bool, len(configurationDefaults))
	for name := range configurationDefaults {
		result[name] = true
	}
	return result
}()

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
	values := make(map[string]configurationValue, len(configurationDefaults))
	for name, value := range configurationDefaults {
		values[name] = configurationValue{value: value, source: "default"}
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

var configurationFlagNames = map[string]string{
	"--data-directory":             "data_directory",
	"--mysql-listen-address":       "mysql_listen_address",
	"--tls-certificate-file":       "tls_certificate_file",
	"--tls-private-key-file":       "tls_private_key_file",
	"--diagnostics-listen-address": "diagnostics_listen_address",
	"--log-format":                 "log_format",
	"--statement-timeout-ms":       "statement_timeout_ms", "--lock-wait-timeout-ms": "lock_wait_timeout_ms",
	"--idle-in-transaction-timeout-ms": "idle_in_transaction_timeout_ms", "--idle-session-timeout-ms": "idle_session_timeout_ms",
	"--execution-memory-limit-bytes": "execution_memory_limit_bytes", "--aggregate-execution-memory-limit-bytes": "aggregate_execution_memory_limit_bytes",
	"--temporary-storage-limit-bytes": "temporary_storage_limit_bytes", "--aggregate-temporary-storage-limit-bytes": "aggregate_temporary_storage_limit_bytes",
	"--max-connections": "max_connections", "--max-allowed-packet": "max_allowed_packet", "--max-prepared-stmt-count": "max_prepared_stmt_count",
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
	if !configurationNames[canonical] {
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
	if strings.HasPrefix(line, "[") {
		return "", "", invalidConfiguration(fmt.Sprintf("config line %d: tables are not supported", lineNumber))
	}
	key, raw, valid := strings.Cut(line, "=")
	if !valid {
		return "", "", invalidConfiguration(fmt.Sprintf("config line %d: expected one key=value pair", lineNumber))
	}
	key = strings.TrimSpace(key)
	if !configurationNames[key] {
		return "", "", invalidConfiguration(fmt.Sprintf("config line %d: unknown setting %q", lineNumber, key))
	}
	value, err := tomlValue(strings.TrimSpace(raw))
	if err != nil {
		return "", "", invalidConfiguration(fmt.Sprintf("config line %d: %s", lineNumber, err))
	}
	if value == "" {
		return "", "", invalidConfiguration("config setting " + key + " has an empty value")
	}
	return key, value, nil
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

func tomlValue(raw string) (string, error) {
	if isQuotedTOMLValue(raw) {
		return quotedTOMLValue(raw)
	}
	if !isBareDecimalTOMLValue(raw) {
		return "", errors.New("invalid TOML value")
	}
	return raw, nil
}

func isQuotedTOMLValue(raw string) bool {
	if len(raw) < 2 {
		return false
	}
	return raw[0] == '"' && raw[len(raw)-1] == '"' || raw[0] == '\'' && raw[len(raw)-1] == '\''
}

func quotedTOMLValue(raw string) (string, error) {
	if raw[0] == '\'' {
		return raw[1 : len(raw)-1], nil
	}
	value, err := strconv.Unquote(raw)
	if err != nil {
		return "", errors.New("invalid quoted value")
	}
	return value, nil
}

func isBareDecimalTOMLValue(raw string) bool {
	return raw != "" && strings.Trim(raw, "0123456789") == ""
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
	numbers                                                                   map[string]int64
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
	numbers, err := configurationNumbers(get)
	if err != nil {
		return configurationParts{}, err
	}
	parts := configurationParts{dataDirectory: paths.dataDirectory, certificate: paths.certificate, key: paths.key, mysqlAddress: addresses.mysql, diagnosticsAddress: addresses.diagnostics, format: format, numbers: numbers}
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
	if parts.numbers["execution_memory_limit_bytes"] > parts.numbers["aggregate_execution_memory_limit_bytes"] {
		return invalidConfiguration("execution_memory_limit_bytes cannot exceed aggregate_execution_memory_limit_bytes")
	}
	if parts.numbers["temporary_storage_limit_bytes"] > parts.numbers["aggregate_temporary_storage_limit_bytes"] {
		return invalidConfiguration("temporary_storage_limit_bytes cannot exceed aggregate_temporary_storage_limit_bytes")
	}
	if parts.diagnosticsAddress != "" && parts.diagnosticsAddress == parts.mysqlAddress {
		return invalidConfiguration("MySQL and diagnostics listeners must use different addresses")
	}
	return nil
}

func (parts configurationParts) options() lifecycle.Options {
	numbers := parts.numbers
	format := parts.format
	if format == "text" {
		format = "human"
	}
	return lifecycle.Options{DataDirectory: parts.dataDirectory, MySQLAddress: parts.mysqlAddress, TLSCertFile: parts.certificate, TLSKeyFile: parts.key, DiagnosticsAddress: parts.diagnosticsAddress, Format: format, StatementTimeoutMilliseconds: numbers["statement_timeout_ms"], LockWaitTimeoutMilliseconds: numbers["lock_wait_timeout_ms"], IdleInTransactionTimeoutMilliseconds: numbers["idle_in_transaction_timeout_ms"], IdleSessionTimeoutMilliseconds: numbers["idle_session_timeout_ms"], ExecutionMemoryLimitBytes: numbers["execution_memory_limit_bytes"], AggregateMemoryLimitBytes: numbers["aggregate_execution_memory_limit_bytes"], TemporaryStorageLimitBytes: numbers["temporary_storage_limit_bytes"], AggregateTemporaryLimitBytes: numbers["aggregate_temporary_storage_limit_bytes"], MaxConnections: int(numbers["max_connections"]), MaxAllowedPacket: numbers["max_allowed_packet"], MaxPreparedStmtCount: int(numbers["max_prepared_stmt_count"]), MySQLEnabled: true}
}

type integerSetting struct {
	name             string
	minimum, maximum int64
}

func configurationNumbers(get func(string) string) (map[string]int64, error) {
	maximum := int64(^uint64(0) >> 1)
	settings := []integerSetting{{"statement_timeout_ms", 1, maximum}, {"lock_wait_timeout_ms", 1, maximum}, {"idle_in_transaction_timeout_ms", 1, maximum}, {"idle_session_timeout_ms", 1, maximum}, {"execution_memory_limit_bytes", 1, maximum}, {"aggregate_execution_memory_limit_bytes", 1, maximum}, {"temporary_storage_limit_bytes", 1, maximum}, {"aggregate_temporary_storage_limit_bytes", 1, maximum}, {"max_connections", 1, 2147483647}, {"max_allowed_packet", 1024, 1073741824}, {"max_prepared_stmt_count", 1, 2147483647}}
	result := make(map[string]int64, len(settings))
	for _, setting := range settings {
		value, err := positiveInteger(setting.name, get(setting.name), setting.minimum, setting.maximum)
		if err != nil {
			return nil, err
		}
		result[setting.name] = value
	}
	return result, nil
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
