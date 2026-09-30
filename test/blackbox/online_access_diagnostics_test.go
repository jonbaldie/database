package blackbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestOnlineAccessDiagnosticsHidePasswordValidity(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := filepath.Join(t.TempDir(), "instance")
	password := "online-access-admin"
	initializeServer(t, runner, directory, password)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()

	admin := newWireClient(t, address, "admin", password)
	defer admin.close()
	mustQuery(t, admin, "CREATE USER 'observer' IDENTIFIED BY 'observer-password-1'")
	mustQuery(t, admin, "GRANT OPERATIONAL_OBSERVATION ON *.* TO 'observer'")
	mustQuery(t, admin, "CREATE USER 'nobody' IDENTIFIED BY 'nobody-password-1'")
	mustQuery(t, admin, "CREATE USER 'locked' IDENTIFIED BY 'locked-password-1'")
	mustQuery(t, admin, "GRANT OPERATIONAL_CONTROL ON *.* TO 'locked'")
	mustQuery(t, admin, "ALTER USER 'locked' ACCOUNT LOCK")

	cases := []struct{ name, account, password string }{
		{"wrong password", "observer", "wrong-password"},
		{"missing account", "ghost", "ghost-password-1"},
		{"locked account", "locked", "locked-password-1"},
		{"no grants", "nobody", "nobody-password-1"},
		{"observation only", "observer", "observer-password-1"},
	}
	for _, command := range []string{"backup", "shutdown"} {
		var baseline []any
		for _, testCase := range cases {
			archive := filepath.Join(t.TempDir(), "denied.tar")
			args := []string{"shutdown", "--yes"}
			if command == "backup" {
				args = []string{"backup", "create", "--output", archive}
			}
			args = append(args, "--address="+address, "--account="+testCase.account, "--password-stdin", "--result=json", "--progress=none")
			denied := runner.RunWithStdin(context.Background(), testCase.password+"\n", args...)
			var result map[string]any
			if err := json.Unmarshal([]byte(denied.Stdout), &result); err != nil {
				t.Fatalf("%s %s: decode result: %v; %#v", command, testCase.name, err, denied)
			}
			if denied.ExitCode != 4 || result["exit_class"] != "access" {
				t.Fatalf("%s %s: exit = %d result = %#v", command, testCase.name, denied.ExitCode, result)
			}
			diagnostics, _ := result["diagnostics"].([]any)
			if baseline == nil {
				baseline = diagnostics
			}
			if !reflect.DeepEqual(diagnostics, baseline) || result["diagnostic"] != "connection failed" {
				t.Errorf("%s %s: diagnostics = %#v, want %#v", command, testCase.name, diagnostics, baseline)
			}
			if _, err := os.Stat(archive); !os.IsNotExist(err) {
				t.Fatalf("%s %s: denied backup left artifact: %v", command, testCase.name, err)
			}
		}
	}
	mustQuery(t, admin, "SELECT 1")
}
