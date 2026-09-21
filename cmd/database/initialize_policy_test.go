package main

import (
	"bytes"
	"os"
	"path/filepath"
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
	}
}
