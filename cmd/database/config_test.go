package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigurationPrecedence(t *testing.T) {
	temporary := t.TempDir()
	file := filepath.Join(temporary, "server.toml")
	if err := os.WriteFile(file, []byte("data_directory = \""+filepath.Join(temporary, "file-data")+"\"\nmax_connections = 10\nlog_format = \"text\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := resolveConfiguration(
		[]string{"--config", file, "--max-connections=12", "--log-format=json"},
		[]string{"DATABASE_SERVER_CONFIG=" + filepath.Join(temporary, "ignored.toml"), "DATABASE_SERVER_MAX_CONNECTIONS=11", "DATABASE_SERVER_MYSQL_LISTEN_ADDRESS=127.0.0.1:3307"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if config.options.DataDirectory != filepath.Join(temporary, "file-data") {
		t.Fatalf("data directory = %q", config.options.DataDirectory)
	}
	if config.options.MaxConnections != 12 || config.options.Format != "json" || config.options.MySQLAddress != "127.0.0.1:3307" {
		t.Fatalf("resolved options = %#v", config.options)
	}
	if config.values["max_connections"].source != "flag" || config.values["log_format"].source != "flag" || config.values["data_directory"].source != "file:"+file {
		t.Fatalf("sources = %#v", config.values)
	}
}

func TestConfigurationOnlyAcceptsCanonicalRegistryForms(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	tests := []struct {
		name string
		args []string
		env  []string
	}{
		{name: "legacy data directory flag", args: []string{"--data-dir=" + directory}},
		{name: "legacy mysql address flag", args: []string{"--data-directory=" + directory, "--mysql-address=127.0.0.1:3306"}},
		{name: "output format is not a registry form", args: []string{"--data-directory=" + directory, "--format=json"}},
		{name: "human log format", args: []string{"--data-directory=" + directory, "--log-format=human"}},
		{name: "lowercase environment suffix", args: []string{"--data-directory=" + directory}, env: []string{"DATABASE_SERVER_max_connections=10"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolveConfiguration(test.args, test.env); err == nil {
				t.Fatal("expected non-canonical configuration form to be rejected")
			}
		})
	}
}

func TestConfigurationRequiresDataDirectoryAndUsesRegistryBounds(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--data-directory=/tmp/database", "--max-connections=2147483648"},
		{"--data-directory=/tmp/database", "--max-prepared-stmt-count=2147483648"},
	} {
		if _, err := resolveConfiguration(args, nil); err == nil {
			t.Fatalf("expected configuration %q to be rejected", args)
		}
	}
}

func TestConfigurationFileAcceptsTOMLStringsContainingEquals(t *testing.T) {
	temporary := t.TempDir()
	file := filepath.Join(temporary, "server.toml")
	if err := os.WriteFile(file, []byte("data_directory = \""+filepath.Join(temporary, "data=directory")+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := resolveConfiguration([]string{"--config=" + file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.options.DataDirectory != filepath.Join(temporary, "data=directory") {
		t.Fatalf("data directory = %q", config.options.DataDirectory)
	}
}

func TestConfigurationFileSelectorMayBeRelative(t *testing.T) {
	temporary := t.TempDir()
	t.Chdir(temporary)
	if err := os.WriteFile("server.toml", []byte("data_directory = \""+filepath.Join(temporary, "instance")+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := resolveConfiguration([]string{"--config=server.toml"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.options.DataDirectory != filepath.Join(temporary, "instance") {
		t.Fatalf("data directory = %q", config.options.DataDirectory)
	}
}

func TestConfigurationFileRejectsNonTOMLStrings(t *testing.T) {
	temporary := t.TempDir()
	file := filepath.Join(temporary, "server.toml")
	if err := os.WriteFile(file, []byte("data_directory = "+filepath.Join(temporary, "instance")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConfiguration([]string{"--config=" + file}, nil); err == nil {
		t.Fatal("expected invalid TOML bare string to be rejected")
	}
}

func TestResolveConfigurationRejectsInvalidSources(t *testing.T) {
	temporary := t.TempDir()
	file := filepath.Join(temporary, "duplicate.toml")
	if err := os.WriteFile(file, []byte("max_connections = 1\nmax_connections = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		env  []string
	}{
		{name: "unknown flag", args: []string{"--not-a-setting=x"}},
		{name: "repeated flag", args: []string{"--max-connections=1", "--max-connections=1"}},
		{name: "unknown environment", env: []string{"DATABASE_SERVER_NOT_A_SETTING=x"}},
		{name: "empty environment", env: []string{"DATABASE_SERVER_MAX_CONNECTIONS="}},
		{name: "duplicate toml", args: []string{"--config", file}},
		{name: "malformed integer", args: []string{"--max-connections=zero"}},
		{name: "contradictory budgets", args: []string{"--execution-memory-limit-bytes=2", "--aggregate-execution-memory-limit-bytes=1"}},
		{name: "one TLS path", args: []string{"--tls-certificate-file=" + filepath.Join(temporary, "cert.pem")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolveConfiguration(test.args, test.env); err == nil {
				t.Fatal("expected configuration rejection")
			}
		})
	}
}

func TestResolveConfigurationClassifiesSourceFailures(t *testing.T) {
	directory := t.TempDir()
	tests := []struct {
		name  string
		args  []string
		env   []string
		class string
	}{
		{name: "unknown flag", args: []string{"--not-a-setting=x"}, class: "invalid_input"},
		{name: "unknown environment", env: []string{"DATABASE_SERVER_NOT_A_SETTING=x"}, class: "invalid_input"},
		{name: "malformed file", args: []string{"--config=" + configurationFile(t, directory, "not a setting")}, class: "invalid_input"},
		{name: "unreadable selected file", args: []string{"--config=" + filepath.Join(directory, "missing.toml")}, class: "precondition"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveConfiguration(test.args, test.env)
			if err == nil {
				t.Fatal("expected configuration rejection")
			}
			if class := configurationClass(err); class != test.class {
				t.Fatalf("configuration class = %q, want %q", class, test.class)
			}
		})
	}
}

func configurationFile(t *testing.T, directory, contents string) string {
	t.Helper()
	path := filepath.Join(directory, "server.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveConfigurationAcceptsBracketedIPv6(t *testing.T) {
	config, err := resolveConfiguration([]string{"--data-directory", "/tmp/database-instance", "--mysql-listen-address", "[::1]:3306"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.options.MySQLAddress != "[::1]:3306" {
		t.Fatalf("address = %q", config.options.MySQLAddress)
	}
}

func TestResolveConfigurationRejectsEquivalentIPv6Listeners(t *testing.T) {
	if _, err := resolveConfiguration([]string{
		"--data-directory=/tmp/database-instance",
		"--mysql-listen-address=[0:0:0:0:0:0:0:1]:3306",
		"--diagnostics-listen-address=[::1]:3306",
	}, nil); err == nil {
		t.Fatal("expected equivalent listener addresses to be rejected")
	}
}

func TestResolveConfigurationPreservesMaximumTimeoutsInMilliseconds(t *testing.T) {
	const maximum = "9223372036854775807"
	config, err := resolveConfiguration([]string{
		"--data-directory=/tmp/database-instance",
		"--statement-timeout-ms=" + maximum,
		"--lock-wait-timeout-ms=" + maximum,
		"--idle-in-transaction-timeout-ms=" + maximum,
		"--idle-session-timeout-ms=" + maximum,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.options.StatementTimeoutMilliseconds != 9223372036854775807 ||
		config.options.LockWaitTimeoutMilliseconds != 9223372036854775807 ||
		config.options.IdleInTransactionTimeoutMilliseconds != 9223372036854775807 ||
		config.options.IdleSessionTimeoutMilliseconds != 9223372036854775807 {
		t.Fatalf("timeout options = %#v", config.options)
	}
}

func TestResolveConfigurationCarriesEveryResourceLimitToServerBoundary(t *testing.T) {
	config, err := resolveConfiguration([]string{
		"--data-directory=/tmp/database-instance",
		"--statement-timeout-ms=11",
		"--lock-wait-timeout-ms=12",
		"--idle-in-transaction-timeout-ms=13",
		"--idle-session-timeout-ms=14",
		"--execution-memory-limit-bytes=15",
		"--aggregate-execution-memory-limit-bytes=16",
		"--temporary-storage-limit-bytes=17",
		"--aggregate-temporary-storage-limit-bytes=18",
		"--max-connections=19",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options := config.options
	if options.StatementTimeoutMilliseconds != 11 || options.LockWaitTimeoutMilliseconds != 12 ||
		options.IdleInTransactionTimeoutMilliseconds != 13 || options.IdleSessionTimeoutMilliseconds != 14 ||
		options.ExecutionMemoryLimitBytes != 15 || options.AggregateMemoryLimitBytes != 16 ||
		options.TemporaryStorageLimitBytes != 17 || options.AggregateTemporaryLimitBytes != 18 ||
		options.MaxConnections != 19 {
		t.Fatalf("resource limits were not preserved: %#v", options)
	}
}
