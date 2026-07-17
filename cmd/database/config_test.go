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
		{name: "statement timeout exceeds duration representation", args: []string{"--statement-timeout-ms=9223372036855"}},
		{name: "lock wait timeout exceeds duration representation", args: []string{"--lock-wait-timeout-ms=9223372036855"}},
		{name: "idle transaction timeout exceeds duration representation", args: []string{"--idle-in-transaction-timeout-ms=9223372036855"}},
		{name: "idle session timeout exceeds duration representation", args: []string{"--idle-session-timeout-ms=9223372036855"}},
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

func TestResolveConfigurationAcceptsBracketedIPv6(t *testing.T) {
	config, err := resolveConfiguration([]string{"--data-dir", "/tmp/database-instance", "--mysql-listen-address", "[::1]:3306"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.options.MySQLAddress != "[::1]:3306" {
		t.Fatalf("address = %q", config.options.MySQLAddress)
	}
}
