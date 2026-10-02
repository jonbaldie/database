package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseCommandOutputRemovesControlsAndRejectsRepeats(t *testing.T) {
	output, args, err := parseCommandOutput([]string{"validate", "--result=json", "--progress", "json", "--data-directory", "/tmp/db"})
	if err != nil {
		t.Fatal(err)
	}
	if output.result != "json" || output.progress != "json" || strings.Join(args, " ") != "validate --data-directory /tmp/db" {
		t.Fatalf("output=%#v args=%q", output, args)
	}
	if _, _, err := parseCommandOutput([]string{"--result=json", "--result=human"}); err == nil {
		t.Fatal("repeated result format was accepted")
	}
}

func TestParseCommandOutputRoutesFormatAliasThroughLegacyReporterMode(t *testing.T) {
	output, args, err := parseCommandOutput([]string{"--format=json", "--data-directory", "/tmp/db"})
	if err != nil {
		t.Fatal(err)
	}
	if output.result != "json" || !output.legacy || !output.formatSet || output.resultSet || strings.Join(args, " ") != "--data-directory /tmp/db" {
		t.Fatalf("format output=%#v args=%q", output, args)
	}

	resultOutput, _, err := parseCommandOutput([]string{"--result=json"})
	if err != nil {
		t.Fatal(err)
	}
	if resultOutput.legacy || !resultOutput.resultSet || resultOutput.formatSet {
		t.Fatalf("--result=json output mode = %#v", resultOutput)
	}
}

func TestOperationReporterCorrelatesJSONProgressAndResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	reporter := newOperationReporter("backup inspect", commandOutput{result: "json", progress: "json"}, &stdout, &stderr)
	reporter.progress("reading")
	if code := reporter.failure("invalid_artifact", "", "invalid backup archive", nil); code != 5 {
		t.Fatalf("exit code=%d, want 5", code)
	}
	var progress, result map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &progress); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	if progress["schema"] != "database.operator.progress/v1" || result["schema"] != "database.operator.result/v1" {
		t.Fatalf("schemas progress=%#v result=%#v", progress["schema"], result["schema"])
	}
	if progress["operation_id"] != result["operation_id"] || progress["command"] != result["command"] {
		t.Fatalf("identity progress=%#v result=%#v", progress, result)
	}
	if result["status"] != "failure" || result["exit_class"] != "invalid_artifact" || result["exit_code"] != float64(5) {
		t.Fatalf("terminal result=%#v", result)
	}
}

func TestOperatorExitCodeClassesAreStable(t *testing.T) {
	for class, want := range map[string]int{
		"success": 0, "invalid_input": 2, "precondition": 3, "access": 4,
		"invalid_artifact": 5, "operation_failed": 6, "interrupted": 7,
	} {
		if got := operatorExitCode(class); got != want {
			t.Errorf("operatorExitCode(%q)=%d, want %d", class, got, want)
		}
	}
}
