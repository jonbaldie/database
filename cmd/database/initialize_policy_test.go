package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestInitializeRejectsCredentialsThatCreateUserRejects(t *testing.T) {
	cases := []struct {
		name     string
		account  string
		password string
	}{
		{name: "short password", account: "admin", password: "secret"},
		{name: "eleven byte password", account: "admin", password: strings.Repeat("p", 11)},
		{name: "long password", account: "admin", password: strings.Repeat("p", 1025)},
		{name: "invalid UTF-8 password", account: "admin", password: "valid-prefix\xff\xfe"},
		{name: "account with host", account: "admin@localhost", password: "contract-valid-password"},
		{name: "account with leading punctuation", account: "_admin", password: "contract-valid-password"},
		{name: "long account", account: strings.Repeat("a", 33), password: "contract-valid-password"},
	}
	outputs := []struct {
		name     string
		flag     string
		wantJSON bool
	}{
		{name: "default human"},
		{name: "format alias json", flag: "--format=json", wantJSON: true},
	}
	for _, test := range cases {
		for _, output := range outputs {
			t.Run(test.name+"/"+output.name, func(t *testing.T) {
				directory := filepath.Join(t.TempDir(), "instance")
				passwordFile := filepath.Join(t.TempDir(), "password")
				if err := os.WriteFile(passwordFile, []byte(test.password), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"init", directory, "--initial-account", test.account, "--password-file", passwordFile}
				if output.flag != "" {
					args = append(args, output.flag)
				}
				var stdout, stderr bytes.Buffer
				code := run(args, &stdout, &stderr)
				if code != 2 {
					t.Fatalf("init exit = %d stdout = %q stderr = %q, want invalid_input exit 2", code, stdout.String(), stderr.String())
				}
				if output.wantJSON {
					if !strings.Contains(stdout.String(), `"schema":"database.operator.result/v1"`) {
						t.Fatalf("init JSON output = %q, want result envelope", stdout.String())
					}
				} else if strings.Contains(stdout.String(), `"schema":"database.operator.result/v1"`) || !strings.HasPrefix(stdout.String(), "init: invalid_input ") {
					t.Fatalf("init default output is not human: stdout = %q stderr = %q", stdout.String(), stderr.String())
				}
				if strings.Contains(stdout.String()+stderr.String(), test.password) && utf8.ValidString(test.password) {
					t.Fatalf("init output reveals the password")
				}
				if _, err := os.Stat(filepath.Join(directory, "instance.json")); !os.IsNotExist(err) {
					t.Fatalf("init wrote instance metadata after rejecting credentials: %v", err)
				}
			})
		}
	}
}

func TestInitializeMissingPasswordUsesRequestedResultFormat(t *testing.T) {
	const wantUsage = "usage: database init --data-directory PATH [--initial-account NAME] (--initial-password-file PATH | --initial-password-stdin) [--result=human|json]"
	for _, output := range []struct {
		name     string
		flag     string
		wantJSON bool
	}{
		{name: "default human"},
		{name: "explicit json", flag: "--result=json", wantJSON: true},
	} {
		t.Run(output.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "instance")
			args := []string{"init", "--data-directory=" + directory, "--initial-account", "admin"}
			if output.flag != "" {
				args = append(args, output.flag)
			}
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("init exit = %d stdout = %q stderr = %q, want invalid_input exit 2", code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("init created or changed target data directory: %v", err)
			}
			if output.wantJSON {
				var result struct {
					Schema     string `json:"schema"`
					RecordType string `json:"record_type"`
					Command    string `json:"command"`
					ExitClass  string `json:"exit_class"`
					ExitCode   int    `json:"exit_code"`
					Diagnostic string `json:"diagnostic"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					t.Fatalf("decode init result %q: %v", stdout.String(), err)
				}
				if result.Schema != "database.operator.result/v1" || result.RecordType != "result" || result.Command != "init" || result.ExitClass != "invalid_input" || result.ExitCode != 2 || result.Diagnostic != wantUsage {
					t.Fatalf("init result = %#v, want invalid_input result envelope with usage %q", result, wantUsage)
				}
				if stderr.Len() != 0 {
					t.Fatalf("init JSON mode wrote to stderr: %q", stderr.String())
				}
				return
			}
			if strings.Contains(stdout.String(), `"schema":"database.operator.result/v1"`) || !strings.HasPrefix(stdout.String(), "init: invalid_input (operation_id=") {
				t.Fatalf("init default output is not human: stdout = %q stderr = %q", stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "init [invalid_input]: "+wantUsage) {
				t.Fatalf("init human diagnostic = %q, want usage %q", stderr.String(), wantUsage)
			}
		})
	}
}

func TestInitializeAcceptsContractBoundaryCredentials(t *testing.T) {
	for _, password := range []string{strings.Repeat("p", 12), strings.Repeat("p", 1024)} {
		directory := filepath.Join(t.TempDir(), "instance")
		passwordFile := filepath.Join(t.TempDir(), "password")
		if err := os.WriteFile(passwordFile, []byte(password), 0o600); err != nil {
			t.Fatal(err)
		}
		account := "A" + strings.Repeat("z", 28) + "._-"
		var stdout, stderr bytes.Buffer
		if code := run([]string{"init", directory, "--initial-account", account, "--password-file", passwordFile}, &stdout, &stderr); code != 0 {
			t.Fatalf("init exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "initialized database instance ") || stderr.Len() != 0 {
			t.Fatalf("init human output changed: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}

func TestInitializeKeepsTextFormatUnsupported(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("valid-password-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", directory, "--password-file", passwordFile, "--format=text"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stdout.String(), `"exit_class":"invalid_input"`) {
		t.Fatalf("init --format=text exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(directory, "instance.json")); !os.IsNotExist(err) {
		t.Fatalf("init created metadata for unsupported output format: %v", err)
	}
}

func TestInitializeReportsStoppedStateAndContractPhases(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "instance")
	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("valid-password-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"init", directory, "--password-file", passwordFile, "--result=json", "--progress=json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	var result struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode init result %q: %v", stdout.String(), err)
	}
	if result.Details["state"] != "stopped" {
		t.Fatalf("init details = %v, want state stopped", result.Details)
	}
	var phases []string
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var record struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode init progress %q: %v", line, err)
		}
		phases = append(phases, record.Phase)
	}
	if want := []string{"preflight", "initializing", "validating"}; !slices.Equal(phases, want) {
		t.Fatalf("init phases = %v, want %v", phases, want)
	}
}
