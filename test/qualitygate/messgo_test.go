package qualitygate_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	messgoVersion = "v0.2.0"
	messgoModule  = "github.com/quality-gates/messgo/cmd/messgo"
)

func TestMessgoGateIsPinnedAndRunsWithQuality(t *testing.T) {
	root := repositoryRoot(t)
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	ruleset := readFile(t, filepath.Join(root, "config", "messgo.xml"))

	for _, want := range []string{
		"MESSGO_VERSION := " + messgoVersion,
		"MESSGO_MODULE := " + messgoModule,
		"MESSGO_PATHS := $(shell $(GO) list -f '{{.Dir}}' ./... | tr '\\n' ',' | sed 's/,$$//')",
		"messgo:",
		"$(GO) run $(MESSGO_MODULE)@$(MESSGO_VERSION) $(MESSGO_PATHS) text $(MESSGO_RULESET) --ignore-tests",
		"quality: fmt-check vet test build messgo",
	} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile does not contain %q", want)
		}
	}
	if !strings.Contains(workflow, "run: make quality") {
		t.Error("CI does not run make quality")
	}
	if !strings.Contains(workflow, "go-version: 1.26.5") {
		t.Error("CI does not pin the Go version required to build messgo")
	}
	if !strings.Contains(workflow, "run: make mutation") || !strings.Contains(workflow, "GITHUB_BASE_SHA") {
		t.Error("CI no longer preserves the changed-code mutation gate")
	}
	for _, want := range []string{"<rule ref=\"codesize\"", "<rule ref=\"design\""} {
		if !strings.Contains(ruleset, want) {
			t.Errorf("messgo ruleset does not include %s", want)
		}
	}
	for _, forbidden := range []string{
		"<exclude",
		"<properties>",
		"<property ",
		"reportLevel",
		"minimum=",
		"maximum=",
		"maxfields=",
	} {
		if strings.Contains(ruleset, forbidden) {
			t.Errorf("messgo ruleset weakens its default rules with %q", forbidden)
		}
	}
}

func TestGovulncheckIsPinnedAndRunsOnPullRequests(t *testing.T) {
	root := repositoryRoot(t)
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "security.yml"))

	for _, want := range []string{
		"GOVULNCHECK_VERSION := v1.1.4",
		"GOVULNCHECK_MODULE := golang.org/x/vuln/cmd/govulncheck",
		"quality: fmt-check vet test build messgo vulncheck",
		"vulncheck:",
		"$(GO) run $(GOVULNCHECK_MODULE)@$(GOVULNCHECK_VERSION) ./...",
	} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile does not contain %q", want)
		}
	}
	for _, want := range []string{
		"name: Security",
		"pull_request:\n    branches: [main]",
		"govulncheck:",
		"run: make vulncheck",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("security workflow does not contain %q", want)
		}
	}
}

func TestGoReportCardEnforcesAPlusOnPullRequests(t *testing.T) {
	root := repositoryRoot(t)
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "goreportcard.yml"))

	for _, requiredMakefileText := range []string{
		"GOCYCLO_VERSION := v0.6.0",
		"INEFFASSIGN_VERSION := v0.2.0",
		"goreportcard:",
		"scripts/goreportcard.py",
	} {
		if !strings.Contains(makefile, requiredMakefileText) {
			t.Errorf("Makefile does not contain %q", requiredMakefileText)
		}
	}
	for _, requiredWorkflowText := range []string{
		"name: Go Report Card",
		"pull_request:",
		"report-card:",
		"name: Enforce A+ grade",
		"run: make goreportcard",
	} {
		if !strings.Contains(workflow, requiredWorkflowText) {
			t.Errorf("Go Report Card workflow does not contain %q", requiredWorkflowText)
		}
	}
}

func TestMessgoReportsDesignViolations(t *testing.T) {
	root := repositoryRoot(t)
	fixture := filepath.Join(root, "test", "qualitygate", "testdata", "goto_violation_test.go")
	ruleset := filepath.Join(root, "config", "messgo.xml")
	command := exec.Command("go", "run", messgoModule+"@"+messgoVersion, fixture, "text", ruleset)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("messgo succeeded for a goto violation")
	}
	if !strings.Contains(string(output), "GotoStatement") {
		t.Fatalf("messgo output does not identify GotoStatement:\n%s", output)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
