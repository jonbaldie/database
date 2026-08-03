package main

import (
	"runtime"
	"testing"
	"time"
)

func TestRunMeetsThresholdsRequiresLatencyThroughputAndVolume(t *testing.T) {
	definition := gateDefinitions[0]
	passing := runEvidence{
		Operations: 100_000,
		Throughput: 5000,
		P95Millis:  10,
		P99Millis:  40,
	}
	if !runMeetsThresholds(passing, 100_000, definition) {
		t.Fatal("expected known-good primary-key run to meet thresholds")
	}
	cases := []struct {
		name string
		run  runEvidence
	}{
		{"low volume", runEvidence{Operations: 99_999, Throughput: 5000, P95Millis: 10, P99Millis: 40}},
		{"slow p95", runEvidence{Operations: 100_000, Throughput: 5000, P95Millis: 10.1, P99Millis: 40}},
		{"slow p99", runEvidence{Operations: 100_000, Throughput: 5000, P95Millis: 10, P99Millis: 40.1}},
		{"low throughput", runEvidence{Operations: 100_000, Throughput: 4999.9, P95Millis: 10, P99Millis: 40}},
		{"errors", runEvidence{Operations: 100_000, Errors: 1, Throughput: 5000, P95Millis: 10, P99Millis: 40}},
		{"unfinished reported only", runEvidence{Operations: 100_000, Unfinished: 50, Throughput: 5000, P95Millis: 10, P99Millis: 40}},
	}
	for _, test := range cases {
		wantPass := test.name == "unfinished reported only"
		if runMeetsThresholds(test.run, 100_000, definition) != wantPass {
			t.Fatalf("%s: expected pass=%v", test.name, wantPass)
		}
	}
}

func TestJudgeRequiresEveryGateAndCleanStart(t *testing.T) {
	passingGate := gateEvidence{Passed: true}
	gates := []gateEvidence{passingGate, passingGate, passingGate, passingGate}
	clean := cleanStartEvidence{Passed: true}
	if got := judge(evidence{Gates: gates, CleanStarts: clean}); got != "accepted" {
		t.Fatalf("judgment=%q, want accepted", got)
	}
	if got := judge(evidence{Gates: gates[:3], CleanStarts: clean}); got != "failed" {
		t.Fatalf("missing gate judgment=%q, want failed", got)
	}
	failed := append([]gateEvidence{}, gates...)
	failed[2].Passed = false
	if got := judge(evidence{Gates: failed, CleanStarts: clean}); got != "failed" {
		t.Fatalf("failed gate judgment=%q, want failed", got)
	}
	if got := judge(evidence{Gates: gates, CleanStarts: cleanStartEvidence{Passed: false}}); got != "failed" {
		t.Fatalf("failed clean start judgment=%q, want failed", got)
	}
}

func TestFixedAcceptanceContractMatchesNormativeDefaults(t *testing.T) {
	cfg, err := parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !fixedAcceptanceContract(cfg) {
		t.Fatal("default config must be the fixed acceptance contract")
	}
	cfg.logicalBytes = 9_999_999_999
	if fixedAcceptanceContract(cfg) {
		t.Fatal("reduced logical bytes must not count as the fixed contract")
	}
}

func TestAcceptanceEnabledRequiresFixedContractReferenceHostAndDefaultStorage(t *testing.T) {
	cfg, err := parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	reference := map[string]string{
		"goos":                  "darwin",
		"machine_model":         "Mac15,5",
		"cpu_model":             "Apple M3",
		"memory_bytes":          "17179869184",
		"reference_environment": "true",
	}
	if !acceptanceEnabled(cfg, reference) {
		t.Fatal("fixed contract on the published reference host should enable acceptance")
	}
	cfg.dataRoot = "/mnt/external"
	if acceptanceEnabled(cfg, reference) {
		t.Fatal("non-default data root must remain diagnostic")
	}
	cfg.dataRoot = ""
	cfg.sessions = 10
	if acceptanceEnabled(cfg, reference) {
		t.Fatal("reduced session count must remain diagnostic")
	}
	cfg.sessions = 50
	linux := map[string]string{
		"goos":                  "linux",
		"machine_model":         "unknown",
		"cpu_model":             "Intel(R) Xeon(R) Processor",
		"memory_bytes":          "17179869184",
		"reference_environment": "false",
	}
	if acceptanceEnabled(cfg, linux) {
		t.Fatal("non-reference hosts must remain diagnostic even when thresholds are enforced")
	}
}

func TestIsReferenceEnvironmentRequiresPublishedMac15_5Envelope(t *testing.T) {
	if isReferenceEnvironment(map[string]string{
		"goos":          "darwin",
		"machine_model": "Mac15,5",
		"cpu_model":     "Apple M3",
		"memory_bytes":  "17179869184",
	}) != true {
		t.Fatal("published Mac15,5 M3 16 GiB host should be the reference environment")
	}
	if isReferenceEnvironment(map[string]string{
		"goos":          "linux",
		"machine_model": "Mac15,5",
		"cpu_model":     "Apple M3",
		"memory_bytes":  "17179869184",
	}) {
		t.Fatal("linux hosts are never the published reference environment")
	}
	if isReferenceEnvironment(map[string]string{
		"goos":          "darwin",
		"machine_model": "Mac14,5",
		"cpu_model":     "Apple M3",
		"memory_bytes":  "17179869184",
	}) {
		t.Fatal("other Mac models are diagnostic only")
	}
}

func TestBenchmarkEnvironmentRecordsConcreteHostFacts(t *testing.T) {
	environment := collectHostEnvironment()
	required := []string{"os_version", "kernel", "cpu_model", "cpu_count", "memory_bytes", "machine_model"}
	for _, key := range required {
		value := environment[key]
		if value == "" {
			t.Fatalf("environment %s is empty", key)
		}
		if value == "unknown" && key != "machine_model" {
			t.Fatalf("environment %s=%q", key, value)
		}
	}
	if runtime.GOOS == "linux" {
		if environment["cpu_model"] == "unknown" || environment["memory_bytes"] == "unknown" {
			t.Fatalf("linux environment incomplete: %#v", environment)
		}
		if isReferenceEnvironment(environment) {
			t.Fatal("linux must not claim the published reference environment")
		}
	}
}

func TestDerivePayloadWidthsHitsExactLogicalByteTarget(t *testing.T) {
	cfg := config{logicalBytes: 200_000, narrowRows: 100, relatedRows: 100, seed: 1}
	widths, err := derivePayloadWidths(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if widths.logicalBytes != cfg.logicalBytes {
		t.Fatalf("logical bytes=%d, want %d", widths.logicalBytes, cfg.logicalBytes)
	}
	var total int64
	for index := int64(1); index <= cfg.narrowRows; index++ {
		total += narrowLogicalBytes(index, payload(widths.narrow, index <= widths.narrowExtra))
	}
	for index := int64(1); index <= cfg.relatedRows; index++ {
		total += relatedLogicalBytes(index, payload(widths.related, index <= widths.relatedExtra))
	}
	if total != cfg.logicalBytes {
		t.Fatalf("accounted logical bytes=%d, want %d", total, cfg.logicalBytes)
	}
}

func TestHistogramPercentilesUseCeilRank(t *testing.T) {
	hist := newHistogram()
	for index := 0; index < 100; index++ {
		hist.add(time.Duration(index+1) * time.Millisecond)
	}
	// The harness reports the closed upper bound of the 100 µs bucket that
	// contains the ceil-rank sample.
	if got := hist.percentile(0.95); got != 95100*time.Microsecond {
		t.Fatalf("p95=%s, want 95.1ms", got)
	}
	if got := hist.percentile(0.99); got != 99100*time.Microsecond {
		t.Fatalf("p99=%s, want 99.1ms", got)
	}
}
