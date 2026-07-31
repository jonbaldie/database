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
	mutagoVersion = "v2.7.7"
	mutagoModule  = "github.com/quality-gates/mutago/v2/cmd/mutago"
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
	if !strings.Contains(workflow, "go-version: 1.26.3") {
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

func TestMutagoGateIsPinnedAndRunsInCI(t *testing.T) {
	root := repositoryRoot(t)
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	script := readFile(t, filepath.Join(root, "scripts", "mutation-threshold.sh"))
	config := readFile(t, filepath.Join(root, "config", "mutago.yml"))

	for _, want := range []string{
		"MUTAGO_VERSION := " + mutagoVersion,
		"MUTAGO_VERSION=$(MUTAGO_VERSION) ./scripts/mutation-threshold.sh",
		"mutago_version=\"${MUTAGO_VERSION:-" + mutagoVersion + "}\"",
		"mutago_config=\"${MUTAGO_CONFIG:-$script_dir/../config/mutago.yml}\"",
		"github.com/quality-gates/mutago/v2/cmd/mutago@${mutago_version}",
		"--config=\"$mutago_config\"",
		"--git-diff-lines",
		"--git-diff-base=\"$base\"",
		"--coverage",
		"--min-covered-msi=\"$threshold\"",
	} {
		if !strings.Contains(makefile+script, want) {
			t.Errorf("Mutago gate does not contain %q", want)
		}
	}
	if !strings.Contains(workflow, "run: make mutation") || !strings.Contains(workflow, "GITHUB_BASE_SHA") {
		t.Error("CI does not run the changed-code Mutago gate with the pull-request base")
	}
	if strings.Contains(workflow+script, "go-mutesting") {
		t.Error("the old go-mutesting command remains in the mutation gate")
	}
	if strings.Contains(script, "--ignore-msi-with-no-mutations") {
		t.Error("the Mutago gate may not pass when it made no mutations")
	}
	if !strings.Contains(config, "json_output: false") {
		t.Error("the Mutago configuration must prevent report.json output")
	}
}

func TestMutagoFailsQualityGateForEscapedCoveredMutation(t *testing.T) {
	root := repositoryRoot(t)
	fixture := filepath.Join(root, "test", "qualitygate", "testdata", "mutago_fixture")
	report := filepath.Join(fixture, "report.json")
	if err := os.Remove(report); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove prior Mutago report: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(report); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove Mutago report: %v", err)
		}
	})
	command := exec.Command(
		"go",
		"run",
		mutagoModule+"@"+mutagoVersion,
		"--noop",
		"--coverage",
		"--min-covered-msi=100",
		"--quiet",
		"--no-diffs",
		"./...",
	)
	command.Dir = fixture
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("Mutago passed a fixture with an escaped covered mutation")
	}
	if !strings.Contains(string(output), "ESCAPED positive.go") || !strings.Contains(string(output), "Covered MSI") {
		t.Fatalf("Mutago did not report the escaped covered mutation:\n%s", output)
	}
}

func TestMutationThresholdScriptRejectsEscapedChangedMutation(t *testing.T) {
	root := repositoryRoot(t)
	fixture, base := mutagoGitFixture(t, `package mutago_fixture

func Positive(value int) bool {
	return value > 0
}
`, `package mutago_fixture

import "testing"

func TestPositiveAcceptsPositiveValue(t *testing.T) {
	if !Positive(1) {
		t.Fatal("expected a positive value to be accepted")
	}
}
`, `package mutago_fixture

func Positive(value int) bool {
	return value >= 0
}
`)
	output, err := runMutationThreshold(t, root, fixture, base)
	if err == nil {
		t.Fatal("the mutation script passed an escaped changed mutation")
	}
	if !strings.Contains(output, "ESCAPED positive.go") || !strings.Contains(output, "Covered MSI") {
		t.Fatalf("the mutation script did not report the escaped changed mutation:\n%s", output)
	}
}

func TestMutationThresholdScriptRejectsChangedCodeWithoutCoveredMutation(t *testing.T) {
	root := repositoryRoot(t)
	fixture, base := mutagoGitFixture(t, `package mutago_fixture

func Covered(value int) bool {
	return value > 0
}
`, `package mutago_fixture

import "testing"

func TestCovered(t *testing.T) {
	if !Covered(1) {
		t.Fatal("expected a positive value to be accepted")
	}
}
`, `package mutago_fixture

func Covered(value int) bool {
	return value > 0
}

func Uncovered(value int) bool {
	return value >= 0
}
`)
	output, err := runMutationThreshold(t, root, fixture, base)
	if err == nil {
		t.Fatal("the mutation script passed changed code with no covered mutation")
	}
	if !strings.Contains(output, "Covered MSI") {
		t.Fatalf("the mutation script did not report a covered-MSI failure:\n%s", output)
	}
}

func mutagoGitFixture(t *testing.T, initialSource, testSource, changedSource string) (string, string) {
	t.Helper()
	fixture := t.TempDir()
	writeFile(t, filepath.Join(fixture, "go.mod"), "module mutago_fixture\n\ngo 1.26.3\n")
	writeFile(t, filepath.Join(fixture, "positive.go"), initialSource)
	writeFile(t, filepath.Join(fixture, "positive_test.go"), testSource)
	runGit(t, fixture, "init", "--initial-branch=main")
	runGit(t, fixture, "config", "user.email", "qualitygate@example.test")
	runGit(t, fixture, "config", "user.name", "Quality Gate")
	runGit(t, fixture, "add", ".")
	runGit(t, fixture, "commit", "--quiet", "--message", "baseline")
	base := strings.TrimSpace(runGit(t, fixture, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(fixture, "positive.go"), changedSource)
	runGit(t, fixture, "add", "positive.go")
	runGit(t, fixture, "commit", "--quiet", "--message", "change")

	return fixture, base
}

func runMutationThreshold(t *testing.T, root, fixture, base string) (string, error) {
	t.Helper()
	report := filepath.Join(fixture, "report.json")
	t.Cleanup(func() {
		if err := os.Remove(report); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove Mutago report: %v", err)
		}
	})
	command := exec.Command(filepath.Join(root, "scripts", "mutation-threshold.sh"))
	command.Dir = fixture
	command.Env = append(os.Environ(), "GITHUB_BASE_SHA="+base, "MUTAGO_VERSION="+mutagoVersion)
	output, err := command.CombinedOutput()
	if _, reportErr := os.Stat(report); reportErr == nil {
		t.Errorf("the mutation script left %s", report)
	} else if !os.IsNotExist(reportErr) {
		t.Errorf("inspect Mutago report: %v", reportErr)
	}
	return string(output), err
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
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
