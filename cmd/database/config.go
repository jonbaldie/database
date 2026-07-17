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
	"time"

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
	values := make(map[string]configurationValue, len(configurationDefaults))
	for name, value := range configurationDefaults {
		values[name] = configurationValue{value: value, source: "default"}
	}

	configPath, flags, err := parseConfigurationFlags(args)
	if err != nil {
		return configuration{}, err
	}
	envValues, envConfigPath, err := parseConfigurationEnvironment(environment)
	if err != nil {
		return configuration{}, err
	}
	if configPath == "" {
		configPath = envConfigPath
	}
	if configPath != "" {
		fileValues, err := readConfigurationFile(configPath)
		if err != nil {
			return configuration{}, err
		}
		for name, value := range fileValues {
			values[name] = configurationValue{value: value, source: "file:" + configPath}
		}
	}
	for name, value := range envValues {
		values[name] = configurationValue{value: value, source: "environment"}
	}
	for name, value := range flags {
		values[name] = configurationValue{value: value, source: "flag"}
	}

	mysqlExplicit := values["mysql_listen_address"].source != "default"
	config, err := makeConfiguration(values, mysqlExplicit)
	if err != nil {
		return configuration{}, err
	}
	// The original diagnostics-only smoke path did not initialize an instance
	// or open the default MySQL port. Keep that invocation usable while the
	// documented server path remains explicit about its data directory.
	config.options.MySQLEnabled = config.options.DataDirectory != "" || mysqlExplicit
	if stateFile, ok := flags["state_file"]; ok {
		config.options.StateFile = stateFile
	}
	if config.options.MySQLEnabled && config.options.DataDirectory == "" {
		return configuration{}, invalidConfiguration("data_directory is required when the MySQL listener is enabled")
	}
	if config.options.DataDirectory == "" && (flags["data_directory"] != "" || envValues["data_directory"] != "" || configPath != "") {
		return configuration{}, invalidConfiguration("data_directory must be a non-empty absolute path")
	}
	return config, nil
}

func parseConfigurationFlags(args []string) (string, map[string]string, error) {
	values := make(map[string]string)
	seen := make(map[string]bool)
	configPath := ""
	configSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" {
			if seen["log_format"] {
				return "", nil, invalidConfiguration("repeated setting log_format")
			}
			seen["log_format"] = true
			values["log_format"] = "json"
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if !hasValue {
			switch name {
			case "--config", "--format", "--log-format", "--data-dir", "--data-directory", "--mysql-address", "--mysql-listen-address", "--tls-cert", "--tls-certificate-file", "--tls-key", "--tls-private-key-file", "--diagnostics-address", "--diagnostics-listen-address", "--state-file":
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return "", nil, invalidConfiguration(name + " requires a non-empty value")
				}
				i++
				value = args[i]
			default:
				return "", nil, invalidConfiguration(fmt.Sprintf("unknown flag %q", arg))
			}
		}
		if value == "" {
			return "", nil, invalidConfiguration(fmt.Sprintf("%s has an empty value", name))
		}
		if name == "--config" {
			if configSeen {
				return "", nil, invalidConfiguration("repeated setting config")
			}
			configSeen = true
			configPath = value
			continue
		}
		if name == "--state-file" {
			if seen["state_file"] {
				return "", nil, invalidConfiguration("repeated setting state_file")
			}
			seen["state_file"] = true
			values["state_file"] = value
			continue
		}
		canonical, normalized, ok := flagSetting(name, value)
		if !ok {
			return "", nil, invalidConfiguration(fmt.Sprintf("unknown flag %q", name))
		}
		if seen[canonical] {
			return "", nil, invalidConfiguration("repeated setting " + canonical)
		}
		seen[canonical] = true
		values[canonical] = normalized
	}
	return configPath, values, nil
}

func flagSetting(name, value string) (string, string, bool) {
	canonical := map[string]string{
		"--data-dir": "data_directory", "--data-directory": "data_directory",
		"--mysql-address": "mysql_listen_address", "--mysql-listen-address": "mysql_listen_address",
		"--tls-cert": "tls_certificate_file", "--tls-certificate-file": "tls_certificate_file",
		"--tls-key": "tls_private_key_file", "--tls-private-key-file": "tls_private_key_file",
		"--diagnostics-address": "diagnostics_listen_address", "--diagnostics-listen-address": "diagnostics_listen_address",
		"--log-format": "log_format", "--format": "log_format",
		"--statement-timeout-ms": "statement_timeout_ms", "--lock-wait-timeout-ms": "lock_wait_timeout_ms",
		"--idle-in-transaction-timeout-ms": "idle_in_transaction_timeout_ms", "--idle-session-timeout-ms": "idle_session_timeout_ms",
		"--execution-memory-limit-bytes": "execution_memory_limit_bytes", "--aggregate-execution-memory-limit-bytes": "aggregate_execution_memory_limit_bytes",
		"--temporary-storage-limit-bytes": "temporary_storage_limit_bytes", "--aggregate-temporary-storage-limit-bytes": "aggregate_temporary_storage_limit_bytes",
		"--max-connections": "max_connections", "--max-allowed-packet": "max_allowed_packet", "--max-prepared-stmt-count": "max_prepared_stmt_count",
	}
	key, ok := canonical[name]
	if !ok {
		return "", "", false
	}
	if key == "log_format" && value == "human" {
		value = "text"
	}
	return key, value, true
}

func parseConfigurationEnvironment(environment []string) (map[string]string, string, error) {
	values := make(map[string]string)
	configPath := ""
	seen := make(map[string]bool)
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(name, "DATABASE_SERVER_") {
			continue
		}
		if name == "DATABASE_SERVER_CONFIG" {
			if seen[name] {
				return nil, "", invalidConfiguration("repeated setting config")
			}
			seen[name] = true
			if strings.TrimSpace(value) == "" {
				return nil, "", invalidConfiguration("DATABASE_SERVER_CONFIG has an empty value")
			}
			configPath = value
			continue
		}
		canonical := strings.ToLower(strings.TrimPrefix(name, "DATABASE_SERVER_"))
		if !configurationNames[canonical] {
			return nil, "", invalidConfiguration(fmt.Sprintf("unknown environment setting %q", name))
		}
		if seen[name] {
			return nil, "", invalidConfiguration("repeated environment setting " + canonical)
		}
		seen[name] = true
		if strings.TrimSpace(value) == "" {
			return nil, "", invalidConfiguration(name + " has an empty value")
		}
		values[canonical] = value
	}
	return values, configPath, nil
}

func readConfigurationFile(path string) (map[string]string, error) {
	if !filepath.IsAbs(path) {
		return nil, invalidConfiguration("config path must be an absolute path")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, &configurationError{class: "precondition", message: fmt.Sprintf("read config file: %v", err)}
	}
	defer file.Close()
	values := make(map[string]string)
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(stripTOMLComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			return nil, invalidConfiguration(fmt.Sprintf("config line %d: tables are not supported", lineNumber))
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok || strings.Contains(raw, "=") {
			return nil, invalidConfiguration(fmt.Sprintf("config line %d: expected one key=value pair", lineNumber))
		}
		key = strings.TrimSpace(key)
		if !configurationNames[key] {
			return nil, invalidConfiguration(fmt.Sprintf("config line %d: unknown setting %q", lineNumber, key))
		}
		if seen[key] {
			return nil, invalidConfiguration("duplicate config setting " + key)
		}
		seen[key] = true
		value, err := tomlValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, invalidConfiguration(fmt.Sprintf("config line %d: %s", lineNumber, err))
		}
		if value == "" {
			return nil, invalidConfiguration("config setting " + key + " has an empty value")
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, &configurationError{class: "precondition", message: fmt.Sprintf("read config file: %v", err)}
	}
	return values, nil
}

func stripTOMLComment(line string) string {
	quoted := byte(0)
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quoted != 0 {
			if quoted == '"' && escaped {
				escaped = false
				continue
			}
			if quoted == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quoted {
				quoted = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quoted = c
		} else if c == '#' {
			return line[:i]
		}
	}
	return line
}

func tomlValue(raw string) (string, error) {
	if len(raw) >= 2 && ((raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
		if raw[0] == '\'' {
			return raw[1 : len(raw)-1], nil
		}
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", errors.New("invalid quoted value")
		}
		return value, nil
	}
	if strings.ContainsAny(raw, "[]{}") || strings.ContainsAny(raw, " \t") {
		return "", errors.New("invalid TOML value")
	}
	return raw, nil
}

func makeConfiguration(values map[string]configurationValue, explicitMySQL bool) (configuration, error) {
	get := func(name string) string { return values[name].value }
	dataDirectory, err := absolutePath("data_directory", get("data_directory"), false)
	if err != nil {
		return configuration{}, err
	}
	mysqlAddress, err := networkAddress("mysql_listen_address", get("mysql_listen_address"))
	if err != nil {
		return configuration{}, err
	}
	diagnosticsAddress := ""
	if get("diagnostics_listen_address") != "" {
		diagnosticsAddress, err = networkAddress("diagnostics_listen_address", get("diagnostics_listen_address"))
		if err != nil {
			return configuration{}, err
		}
	}
	cert, err := absolutePath("tls_certificate_file", get("tls_certificate_file"), true)
	if err != nil {
		return configuration{}, err
	}
	key, err := absolutePath("tls_private_key_file", get("tls_private_key_file"), true)
	if err != nil {
		return configuration{}, err
	}
	if (cert == "") != (key == "") {
		return configuration{}, invalidConfiguration("tls_certificate_file and tls_private_key_file must be provided together")
	}
	if cert != "" {
		if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
			return configuration{}, invalidConfiguration(fmt.Sprintf("invalid TLS certificate/key pair: %v", err))
		}
	}
	format := get("log_format")
	if format != "json" && format != "text" {
		return configuration{}, invalidConfiguration("log_format must be json or text")
	}
	positive := func(name string) (int64, error) { return positiveInteger(name, get(name), 1, int64(^uint64(0)>>1)) }
	statement, err := positive("statement_timeout_ms")
	if err != nil {
		return configuration{}, err
	}
	lockWait, err := positive("lock_wait_timeout_ms")
	if err != nil {
		return configuration{}, err
	}
	idleTransaction, err := positive("idle_in_transaction_timeout_ms")
	if err != nil {
		return configuration{}, err
	}
	idleSession, err := positive("idle_session_timeout_ms")
	if err != nil {
		return configuration{}, err
	}
	memory, err := positive("execution_memory_limit_bytes")
	if err != nil {
		return configuration{}, err
	}
	aggregateMemory, err := positive("aggregate_execution_memory_limit_bytes")
	if err != nil {
		return configuration{}, err
	}
	temporary, err := positive("temporary_storage_limit_bytes")
	if err != nil {
		return configuration{}, err
	}
	aggregateTemporary, err := positive("aggregate_temporary_storage_limit_bytes")
	if err != nil {
		return configuration{}, err
	}
	connections, err := positiveInteger("max_connections", get("max_connections"), 1, int64(^uint(0)>>1))
	if err != nil {
		return configuration{}, err
	}
	packet, err := positiveInteger("max_allowed_packet", get("max_allowed_packet"), 1024, 1073741824)
	if err != nil {
		return configuration{}, err
	}
	prepared, err := positiveInteger("max_prepared_stmt_count", get("max_prepared_stmt_count"), 1, int64(^uint(0)>>1))
	if err != nil {
		return configuration{}, err
	}
	if memory > aggregateMemory {
		return configuration{}, invalidConfiguration("execution_memory_limit_bytes cannot exceed aggregate_execution_memory_limit_bytes")
	}
	if temporary > aggregateTemporary {
		return configuration{}, invalidConfiguration("temporary_storage_limit_bytes cannot exceed aggregate_temporary_storage_limit_bytes")
	}
	if diagnosticsAddress != "" && diagnosticsAddress == mysqlAddress {
		return configuration{}, invalidConfiguration("MySQL and diagnostics listeners must use different addresses")
	}
	if explicitMySQL && dataDirectory == "" {
		return configuration{}, invalidConfiguration("data_directory is required when the MySQL listener is enabled")
	}
	options := lifecycle.Options{
		DataDirectory: dataDirectory, MySQLAddress: mysqlAddress, TLSCertFile: cert, TLSKeyFile: key,
		DiagnosticsAddress: diagnosticsAddress, Format: format, StatementTimeout: time.Duration(statement) * time.Millisecond,
		LockWaitTimeout: time.Duration(lockWait) * time.Millisecond, IdleInTransactionTimeout: time.Duration(idleTransaction) * time.Millisecond,
		IdleSessionTimeout: time.Duration(idleSession) * time.Millisecond, ExecutionMemoryLimitBytes: memory,
		AggregateMemoryLimitBytes: aggregateMemory, TemporaryStorageLimitBytes: temporary,
		AggregateTemporaryLimitBytes: aggregateTemporary, MaxConnections: int(connections), MaxAllowedPacket: packet,
		MaxPreparedStmtCount: int(prepared), MySQLEnabled: dataDirectory != "" || explicitMySQL,
	}
	if format == "text" {
		options.Format = "human"
	}
	return configuration{options: options, values: values}, nil
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
	if err != nil || host == "" || net.ParseIP(host) == nil {
		return "", invalidConfiguration(name + " must be an IPv4 address or bracketed IPv6 address with a port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", invalidConfiguration(name + " port must be between 1 and 65535")
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + strconv.Itoa(portNumber), nil
	}
	return host + ":" + strconv.Itoa(portNumber), nil
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

func configurationResult(config configuration) map[string]any {
	settings := make(map[string]any, len(config.values))
	for name, setting := range config.values {
		value := setting.value
		if name == "tls_private_key_file" && value != "" {
			value = "[redacted]"
		}
		settings[name] = map[string]string{"value": value, "source": setting.source}
	}
	return map[string]any{"schema": "database.configuration/v1", "settings": settings}
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
