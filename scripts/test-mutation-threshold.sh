#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
gate_script="$script_dir/mutation-threshold.sh"
fixture_dir="$script_dir/../testdata/mutation-threshold"

if ! command -v mutago >/dev/null 2>&1; then
	echo "mutation threshold test: install mutago before running this test" >&2
	exit 1
fi

temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT
export GOCACHE="${GOCACHE:-$temporary_directory/cache}"
workspace="$temporary_directory/project"
mkdir -p "$workspace"
cp "$fixture_dir/go.mod" "$workspace/"
printf 'package mutationprobe\n\nfunc Unchanged(n int) bool { return n > 100 }\n' > "$workspace/positive.go"

cd "$workspace"
git init -q
git config user.email mutation-threshold-test@example.invalid
git config user.name "Mutation Threshold Test"
git add go.mod positive.go
git commit -qm "baseline with unrelated untested code"
base="$(git rev-parse HEAD)"
tail -n +2 "$fixture_dir/positive.go.fixture" >> positive.go
git add positive.go
git commit -qm "add changed production code"

expected_source="$temporary_directory/positive.go.expected"
cp positive.go "$expected_source"
printf 'existing report\n' > report.json
cp report.json "$temporary_directory/report.expected"
no_tests_output="$temporary_directory/no-tests.log"
set +e
MUTATION_THRESHOLD=0.80 GITHUB_BASE_SHA="$base" GOMAXPROCS=2 GOFLAGS='-p=2' "$gate_script" >"$no_tests_output" 2>&1
no_tests_status=$?
set -e
cat "$no_tests_output"
if [[ "$no_tests_status" -ne 4 ]]; then
	echo "mutation threshold regression: no-test project did not fail with gate exit 4 (got $no_tests_status)" >&2
	exit 1
fi
if ! grep -Fq 'mutation score is 0.00%' "$no_tests_output" || ! grep -Fq 'below minimum required 80.00%' "$no_tests_output"; then
	echo "mutation threshold regression: no-test project did not report MSI below the threshold" >&2
	exit 1
fi
if ! cmp -s "$expected_source" positive.go; then
	echo "mutation threshold regression: source was not restored after the failing gate run" >&2
	exit 1
fi

echo "mutation threshold regression: no-test project was rejected"

cat > positive_test.go <<'EOF'
package mutationprobe

import "testing"

func TestIsPositiveBoundaries(t *testing.T) {
	for _, test := range []struct {
		name  string
		input int
		want  bool
	}{
		{name: "negative", input: -1, want: false},
		{name: "zero", input: 0, want: false},
		{name: "positive", input: 1, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsPositive(test.input); got != test.want {
				t.Errorf("IsPositive(%d) = %t, want %t", test.input, got, test.want)
			}
		})
	}
}
EOF

tests_output="$temporary_directory/tests.log"
set +e
MUTATION_THRESHOLD=0.80 GITHUB_BASE_SHA="$base" GOMAXPROCS=2 GOFLAGS='-p=2' "$gate_script" >"$tests_output" 2>&1
tests_status=$?
set -e
cat "$tests_output"
if [[ "$tests_status" -ne 0 ]]; then
	echo "mutation threshold regression: boundary tests did not pass the 0.80 gate" >&2
	exit 1
fi
if ! grep -Fq 'mutation score is 100.00%' "$tests_output" || ! grep -Fq 'covered-code mutation score is 100.00%' "$tests_output"; then
	echo "mutation threshold regression: boundary tests did not kill all fixture mutants" >&2
	exit 1
fi
if ! cmp -s "$expected_source" positive.go; then
	echo "mutation threshold regression: source was not restored after the passing gate run" >&2
	exit 1
fi

echo "mutation threshold regression: boundary tests killed all changed-line mutants"
echo "mutation threshold regression: unchanged untested function was excluded"
echo "mutation threshold regression: source was restored after both runs"

if GITHUB_BASE_SHA=missing-base "$gate_script" > "$temporary_directory/invalid-base.log" 2>&1; then
	echo "mutation threshold regression: an invalid base passed the gate" >&2
	exit 1
fi
echo "mutation threshold regression: an invalid base was rejected"

if ! cmp -s "$temporary_directory/report.expected" report.json; then
	echo "mutation threshold regression: an existing report was overwritten" >&2
	exit 1
fi
echo "mutation threshold regression: an existing report was preserved"

git add positive_test.go
git commit -qm "add boundary tests"
local_base="$(git rev-parse HEAD)"
printf 'package mutationprobe\n\nfunc Unchanged(n int) bool { return n > 100 }\n\nfunc IsPositive(n int) bool { return n >= 1 }\n' > positive.go
git add positive.go
GITHUB_BASE_SHA="$local_base" "$gate_script" > "$temporary_directory/staged.log" 2>&1
if ! grep -Fq 'mutation score is 100.00%' "$temporary_directory/staged.log"; then
	echo "mutation threshold regression: staged production lines were not checked" >&2
	exit 1
fi
git restore --staged positive.go
GITHUB_BASE_SHA="$local_base" "$gate_script" > "$temporary_directory/unstaged.log" 2>&1
if ! grep -Fq 'mutation score is 100.00%' "$temporary_directory/unstaged.log"; then
	echo "mutation threshold regression: unstaged production lines were not checked" >&2
	exit 1
fi
echo "mutation threshold regression: staged and unstaged lines were checked"

cat > slow_test.go <<'EOF'
package mutationprobe

import (
	"testing"
	"time"
)

func TestSlowHealthySuite(t *testing.T) {
	time.Sleep(11 * time.Second)
}
EOF
GITHUB_BASE_SHA="$local_base" "$gate_script" > "$temporary_directory/slow-suite.log" 2>&1
if ! grep -Fq 'mutation score is 100.00%' "$temporary_directory/slow-suite.log"; then
	echo "mutation threshold regression: a healthy suite slower than ten seconds was rejected" >&2
	exit 1
fi
echo "mutation threshold regression: the mutant budget follows the healthy baseline"
