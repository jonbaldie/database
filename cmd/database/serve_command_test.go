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
		{State: "ready", InstanceID: "instance-497", DiagnosticsAddress: "127.0.0.1:9"},
		{State: "stopping", ShutdownReason: "signal"},
		{State: "stopped"},
		{State: "failed"},
	} {
		recordServeEvent(reporter, event, details)
	}

	var phases []string
	decoder := json.NewDecoder(&stderr)
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		phase, _ := record["phase"].(string)
		phases = append(phases, phase)
	}
	if got := strings.Join(phases, ","); got != "recovering,ready,stopping" {
		t.Fatalf("serve progress phases = %q, want recovering,ready,stopping", got)
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
