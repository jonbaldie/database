package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainExitsWithTheCommandResult(t *testing.T) {
	previousArgs, previousExit := os.Args, exitProcess
	t.Cleanup(func() {
		os.Args = previousArgs
		exitProcess = previousExit
	})
	os.Args = []string{"database", "unknown"}
	exitCode := -1
	exitProcess = func(code int) { exitCode = code }

	main()

	if exitCode != 2 {
		t.Fatalf("process exit code = %d, want 2", exitCode)
	}
}

func TestUnsupportedOperatorOperationsFailExplicitly(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		exitCode  int
		exitClass string
		operation string
	}{
		{name: "upgrade", args: []string{"upgrade"}, exitCode: 1, exitClass: "operation_failed", operation: "upgrade"},
		{name: "data validate", args: []string{"data", "validate"}, exitCode: 1, exitClass: "operation_failed", operation: "data validate"},
		{name: "data inspect", args: []string{"data", "inspect"}, exitCode: 1, exitClass: "operation_failed", operation: "data inspect"},
		{name: "config inspect", args: []string{"config", "inspect", "--format=json"}, exitCode: 2, exitClass: "invalid_input", operation: "config inspect"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != test.exitCode {
				t.Fatalf("exit code = %d, want %d; stdout=%q stderr=%q", code, test.exitCode, stdout.String(), stderr.String())
			}
			var result map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("result is not JSON: %v; output=%q", err, stdout.String())
			}
			if result["schema"] != "database.operator.result/v1" || result["operation"] != test.operation {
				t.Fatalf("result identity = %#v", result)
			}
			if result["success"] != false || result["exit_class"] != test.exitClass {
				t.Fatalf("result outcome = %#v", result)
			}
			if operationID, ok := result["operation_id"].(string); !ok || operationID == "" {
				t.Fatalf("operation_id = %#v", result["operation_id"])
			}
		})
	}
}

func TestOperatorRejectsUnknownFlags(t *testing.T) {
	var stdout bytes.Buffer
	if code := run([]string{"backup", "inspect", "--not-a-flag=value"}, &stdout, &bytes.Buffer{}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["exit_class"] != "invalid_input" || !strings.Contains(result["diagnostic"].(string), "unknown flag") {
		t.Fatalf("result = %#v", result)
	}
}

func TestCommandHandlersRouteEverySupportedWorkflow(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		handler commandHandler
	}{
		{name: "version", args: []string{"version"}, handler: versionCommand},
		{name: "initialization", args: []string{"init"}, handler: initializeCommand},
		{name: "backup", args: []string{"backup", "inspect"}, handler: operatorCommandHandler},
		{name: "restore", args: []string{"restore"}, handler: operatorCommandHandler},
		{name: "upgrade", args: []string{"upgrade"}, handler: operatorCommandHandler},
		{name: "data", args: []string{"data", "validate"}, handler: operatorCommandHandler},
		{name: "shutdown", args: []string{"shutdown"}, handler: operatorCommandHandler},
		{name: "configuration", args: []string{"config", "validate"}, handler: configCommandHandler},
		{name: "serve", args: []string{"serve"}, handler: serveCommand},
		{name: "help", args: []string{"help"}, handler: helpCommand},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, ok := commandHandlerFor(test.args[0])
			if !ok {
				t.Fatalf("handler missing for %q", test.args[0])
			}
			if fmt.Sprintf("%p", handler) != fmt.Sprintf("%p", test.handler) {
				t.Fatalf("handler for %q changed workflow ownership", test.args[0])
			}
		})
	}
}

func TestCommandHandlersRejectUnknownCommand(t *testing.T) {
	if _, ok := commandHandlerFor("unknown"); ok {
		t.Fatal("unknown command received a handler")
	}
}

func TestConfigValidateHumanOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"config", "validate", "--format=text", "--data-directory=" + filepath.Join(t.TempDir(), "instance")}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "configuration valid") {
		t.Fatalf("human output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "operation_id=op-") {
		t.Fatalf("human output lacks operation identity: %q", stdout.String())
	}
}

func TestConfigValidateJSONOutputIncludesOperationIdentity(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"config", "validate", "--format=json", "--data-directory=" + filepath.Join(t.TempDir(), "instance")}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("result is not JSON: %v; output=%q", err, stdout.String())
	}
	if result["schema"] != "database.configuration/v1" || result["operation"] != "config validate" || result["success"] != true || result["exit_class"] != "success" {
		t.Fatalf("result = %#v", result)
	}
	if operationID, ok := result["operation_id"].(string); !ok || operationID == "" {
		t.Fatalf("operation_id = %#v", result["operation_id"])
	}
	if _, ok := result["settings"].(map[string]any); !ok {
		t.Fatalf("settings = %#v", result["settings"])
	}
}

func TestConfigValidateJSONFailureIncludesStableExitClass(t *testing.T) {
	var stdout bytes.Buffer
	if code := run([]string{"config", "validate", "--format=json", "--unknown-setting=value"}, &stdout, &bytes.Buffer{}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["schema"] != "database.operator.result/v1" || result["exit_class"] != "invalid_input" || result["success"] != false {
		t.Fatalf("result = %#v", result)
	}
	if operationID, ok := result["operation_id"].(string); !ok || operationID == "" {
		t.Fatalf("operation_id = %#v", result["operation_id"])
	}
}

func TestConfigOutputFormatPreservesConfigurationArguments(t *testing.T) {
	format, arguments, err := configOutputFormat([]string{"validate", "--format", "json", "--data-directory=/tmp/database"})
	if err != nil {
		t.Fatal(err)
	}
	if format != "json" {
		t.Fatalf("format = %q", format)
	}
	if got, want := strings.Join(arguments, " "), "validate --data-directory=/tmp/database"; got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
}

func TestConfigOutputFormatRejectsRepeatedAndMissingValues(t *testing.T) {
	for _, arguments := range [][]string{{"--format=json", "--format=text"}, {"--format"}} {
		if _, _, err := configOutputFormat(arguments); err == nil {
			t.Fatalf("configOutputFormat(%q) accepted invalid output format", arguments)
		}
	}
}
