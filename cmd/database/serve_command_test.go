package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jonbaldie/database/internal/lifecycle"
)

func TestRecordServeEventKeepsProgressInServeVocabularyAndCollectsDetails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	reporter := newOperationReporter("serve", commandOutput{result: "json", progress: "json", resultSet: true, progressSet: true}, &stdout, &stderr)
	details := map[string]any{}
	before := time.Now().UTC()
	for _, event := range []lifecycle.Event{
		{State: "recovering", Recovered: true},
		{Schema: "database.lifecycle/v1", State: "ready", InstanceID: "instance-497", DiagnosticsAddress: "127.0.0.1:9"},
		{State: "stopping", ShutdownReason: "signal"},
		{State: "stopped"},
		{State: "failed"},
	} {
		recordServeEvent(reporter, event, details)
	}

	var phases, lifecycleStates []string
	decoder := json.NewDecoder(&stderr)
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record["schema"] == "database.lifecycle/v1" {
			state, _ := record["state"].(string)
			lifecycleStates = append(lifecycleStates, state)
			continue
		}
		phase, _ := record["phase"].(string)
		phases = append(phases, phase)
	}
	if got := strings.Join(phases, ","); got != "recovering,ready,stopping" {
		t.Fatalf("serve progress phases = %q, want recovering,ready,stopping", got)
	}
	if got := strings.Join(lifecycleStates, ","); got != "ready" {
		t.Fatalf("serve lifecycle records on stderr = %q, want only ready", got)
	}
	if stdout.Len() != 0 {
		t.Fatalf("serve stdout before the result = %q, want empty", stdout.String())
	}
	if details["instance_id"] != "instance-497" || details["shutdown_reason"] != "signal" || details["diagnostics_address"] != "127.0.0.1:9" {
		t.Fatalf("serve details = %#v", details)
	}
	for _, key := range []string{"ready_at", "stopping_at"} {
		text, _ := details[key].(string)
		recorded, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || recorded.Before(before.Truncate(time.Microsecond)) || !strings.HasSuffix(text, "Z") {
			t.Fatalf("details[%q] = %#v, want a UTC RFC 3339 time after %s", key, details[key], before)
		}
	}
}

func TestRecordServeEventOmitsUnknownInstanceIdentity(t *testing.T) {
	reporter := newOperationReporter("serve", commandOutput{result: "json", progress: "none", resultSet: true, progressSet: true}, &bytes.Buffer{}, &bytes.Buffer{})
	details := map[string]any{}
	recordServeEvent(reporter, lifecycle.Event{State: "ready"}, details)
	if _, found := details["instance_id"]; found {
		t.Fatalf("serve details = %#v, want no instance_id without an instance", details)
	}
}

func TestIsServeProgressPhaseAcceptsOnlyTheClosedVocabulary(t *testing.T) {
	for _, phase := range []string{"starting", "recovering", "ready", "stopping"} {
		if !isServeProgressPhase(phase) {
			t.Fatalf("isServeProgressPhase(%q) = false", phase)
		}
	}
	for _, state := range []string{"stopped", "failed", ""} {
		if isServeProgressPhase(state) {
			t.Fatalf("isServeProgressPhase(%q) = true", state)
		}
	}
}

func TestRecordServeEventWritesReadyWarningToStderrForHumanOperatorResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	reporter := newOperationReporter("serve", commandOutput{result: "human", progress: "none", progressSet: true}, &stdout, &stderr)
	warning := lifecycle.Warning{Code: "UNSAFE_NON_TLS_LISTENER", Severity: "warning", Summary: "MySQL listener is reachable beyond loopback without TLS"}
	recordServeEvent(reporter, lifecycle.Event{State: "ready", Warnings: []lifecycle.Warning{warning}}, map[string]any{})
	want := "database: WARNING [UNSAFE_NON_TLS_LISTENER] MySQL listener is reachable beyond loopback without TLS\n"
	if stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want only the warning on stderr", stdout.String(), stderr.String())
	}
}

func TestRecordServeEventWritesReadyLifecycleRecordToStderrForJSONOperatorResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	reporter := newOperationReporter("serve", commandOutput{result: "json", progress: "none", resultSet: true, progressSet: true}, &stdout, &stderr)
	recordServeEvent(reporter, lifecycle.Event{Schema: "database.lifecycle/v1", State: "ready"}, map[string]any{})
	var record lifecycle.Event
	if err := json.Unmarshal(stderr.Bytes(), &record); err != nil || stdout.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q (%v), want one lifecycle record on stderr", stdout.String(), stderr.String(), err)
	}
	if record.State != "ready" || record.OperationID != reporter.id {
		t.Fatalf("ready record = %#v, want state ready and operation_id %q", record, reporter.id)
	}
}

func TestRecordServeEventKeepsLegacyOutputOnStdout(t *testing.T) {
	warning := lifecycle.Warning{Code: "UNSAFE_NON_TLS_LISTENER", Severity: "warning", Summary: "MySQL listener is reachable beyond loopback without TLS"}
	event := lifecycle.Event{Schema: "database.lifecycle/v1", State: "ready", DiagnosticsAddress: "127.0.0.1:9", Warnings: []lifecycle.Warning{warning}}

	var jsonStdout, jsonStderr bytes.Buffer
	jsonReporter := newOperationReporter("serve", commandOutput{result: "json", progress: "none", legacy: true, formatSet: true}, &jsonStdout, &jsonStderr)
	recordServeEvent(jsonReporter, event, map[string]any{})
	var record lifecycle.Event
	if err := json.Unmarshal(jsonStdout.Bytes(), &record); err != nil || jsonStderr.Len() != 0 {
		t.Fatalf("--format=json stdout=%q stderr=%q (%v)", jsonStdout.String(), jsonStderr.String(), err)
	}
	if record.OperationID != jsonReporter.id || len(record.Warnings) != 1 || record.Warnings[0].Code != warning.Code {
		t.Fatalf("--format=json ready record = %#v", record)
	}

	var humanStdout, humanStderr bytes.Buffer
	humanReporter := newOperationReporter("serve", commandOutput{result: "human", progress: "none", legacy: true}, &humanStdout, &humanStderr)
	recordServeEvent(humanReporter, event, map[string]any{})
	want := "database: WARNING [UNSAFE_NON_TLS_LISTENER] MySQL listener is reachable beyond loopback without TLS\ndatabase: ready (diagnostics=127.0.0.1:9)\n"
	if humanStdout.String() != want || humanStderr.Len() != 0 {
		t.Fatalf("human stdout=%q stderr=%q, want %q", humanStdout.String(), humanStderr.String(), want)
	}
}
