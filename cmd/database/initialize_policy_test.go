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
	for _, test := range cases {
		for _, format := range []string{"", "--format=json"} {
			t.Run(test.name+format, func(t *testing.T) {
				directory := filepath.Join(t.TempDir(), "instance")
				passwordFile := filepath.Join(t.TempDir(), "password")
				if err := os.WriteFile(passwordFile, []byte(test.password), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"init", directory, "--initial-account", test.account, "--password-file", passwordFile}
				if format != "" {
					args = append(args, format)
				}
				var stdout, stderr bytes.Buffer
				code := run(args, &stdout, &stderr)
				if code != 2 || !strings.Contains(stdout.String(), `"exit_class":"invalid_input"`) {
					t.Fatalf("init exit = %d stdout = %q stderr = %q, want invalid_input exit 2", code, stdout.String(), stderr.String())
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
