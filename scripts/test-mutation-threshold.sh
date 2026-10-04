#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
gate_script="$script_dir/mutation-threshold.sh"
fixture_dir="$script_dir/../testdata/mutation-threshold"

if ! command -v go-mutesting >/dev/null 2>&1; then
	echo "mutation threshold test: install go-mutesting before running this test" >&2
	exit 1
fi

temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT
workspace="$temporary_directory/project"
mkdir -p "$workspace"
cp "$fixture_dir/go.mod" "$fixture_dir/positive.go" "$workspace/"

cd "$workspace"
git init -q
git config user.email mutation-threshold-test@example.invalid
git config user.name "Mutation Threshold Test"
git add go.mod
git commit -qm "baseline without production code"
base="$(git rev-parse HEAD)"
git add positive.go
git commit -qm "add changed production code"

expected_source="$temporary_directory/positive.go.expected"
cp positive.go "$expected_source"
no_tests_output="$temporary_directory/no-tests.log"
set +e
GITHUB_BASE_SHA="$base" GOMAXPROCS=2 GOFLAGS='-p=2' "$gate_script" >"$no_tests_output" 2>&1
no_tests_status=$?
set -e
cat "$no_tests_output"
if [[ "$no_tests_status" -eq 0 ]]; then
	echo "mutation threshold regression: no-test project passed the 0.80 gate" >&2
	exit 1
fi
if ! grep -Fq 'mutation score is 0.000000' "$no_tests_output" || ! grep -Fq 'mutation threshold: 0.00 is below required 0.80' "$no_tests_output"; then
	echo "mutation threshold regression: no-test project did not report surviving mutants below the threshold" >&2
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
GITHUB_BASE_SHA="$base" GOMAXPROCS=2 GOFLAGS='-p=2' "$gate_script" >"$tests_output" 2>&1
tests_status=$?
set -e
cat "$tests_output"
if [[ "$tests_status" -ne 0 ]]; then
	echo "mutation threshold regression: boundary tests did not pass the 0.80 gate" >&2
	exit 1
fi
if ! grep -Fq 'mutation score is 1.000000' "$tests_output" || ! grep -Fq 'mutation threshold: 1.00 meets required 0.80' "$tests_output"; then
	echo "mutation threshold regression: boundary tests did not kill all fixture mutants" >&2
	exit 1
fi
if ! cmp -s "$expected_source" positive.go; then
	echo "mutation threshold regression: source was not restored after the passing gate run" >&2
	exit 1
fi

echo "mutation threshold regression: boundary tests killed all mutants"
echo "mutation threshold regression: source was restored after both runs"
