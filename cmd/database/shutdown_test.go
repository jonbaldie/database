package main

import (
	"os"
	"strings"
	"testing"
)

func TestShutdownRequiresAccountWithYesAndResultJSON(t *testing.T) {
	result, code := operatorResultForTest(t, []string{"shutdown", "--yes", "--result=json"})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; result=%#v", code, result)
	}
	if result["operation"] != "shutdown" || result["exit_class"] != "invalid_input" {
		t.Fatalf("result = %#v", result)
	}
	diagnostic, _ := result["diagnostic"].(string)
	if strings.Contains(diagnostic, "unknown operator flag") {
		t.Fatalf("diagnostic still reports unknown operator flag: %#v", result)
	}
	if !strings.Contains(diagnostic, "--account") {
		t.Fatalf("diagnostic = %q, want missing --account guidance", diagnostic)
	}
}

func TestShutdownRequiresYesForNonInteractiveUse(t *testing.T) {
	passwordFile := writeShutdownPassword(t, "shutdown-secret")
	result, code := operatorResultForTest(t, []string{
		"shutdown",
		"--account=admin",
		"--password-file", passwordFile,
		"--result=json",
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; result=%#v", code, result)
	}
	if result["exit_class"] != "invalid_input" {
		t.Fatalf("result = %#v", result)
	}
	diagnostic, _ := result["diagnostic"].(string)
	if !strings.Contains(diagnostic, "--yes") {
		t.Fatalf("diagnostic = %q, want --yes requirement", diagnostic)
	}
}

func writeShutdownPassword(t *testing.T, password string) string {
	t.Helper()
	path := t.TempDir() + "/password"
	if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
