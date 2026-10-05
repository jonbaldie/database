#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
gate_script="$script_dir/mutation-threshold.sh"
fixture_dir="$script_dir/../testdata/mutation-threshold"
workspace="$(mktemp -d)"
trap 'rm -rf "$workspace"' EXIT

setup_fixture() {
	local name="$1"
	local mode="$2"
	local project="$workspace/$name"
	mkdir -p "$project"
	cp "$fixture_dir/go.mod" "$project/"
	cp "$fixture_dir/deleted.go.fixture" "$project/deleted.go"
	if [[ "$mode" == no-mutations ]]; then
		cp "$fixture_dir/positive.go.fixture" "$project/positive.go"
		cp "$fixture_dir/positive_test.go.fixture" "$project/positive_test.go"
	else
		cp "$fixture_dir/positive-baseline.go.fixture" "$project/positive.go"
	fi

	cd "$project"
	git init -q
	git config user.email mutation-threshold-test@example.invalid
	git config user.name "Mutation Threshold Test"
	git add go.mod deleted.go positive.go
	[[ ! -f positive_test.go ]] || git add positive_test.go
	git commit -qm "baseline with production code"
	local base
	base="$(git rev-parse HEAD)"
	if [[ "$mode" == no-mutations ]]; then
		printf '\n// Changed documentation only.\n' >> positive.go
	else
		cp "$fixture_dir/positive.go.fixture" positive.go
		case "$mode" in
		tested)
			cp "$fixture_dir/positive_test.go.fixture" positive_test.go
			;;
		weak-tests)
			cp "$fixture_dir/positive_weak_test.go.fixture" positive_test.go
			;;
		esac
	fi
	rm deleted.go
	git add -A
	git commit -qm "change production code"
	cp positive.go "$workspace/$name.expected"
	printf '%s\n' "$base" > "$workspace/$name.base"
}

run_gate() {
	local name="$1"
	local output="$workspace/$name.log"
	local base
	base="$(<"$workspace/$name.base")"
	set +e
	GITHUB_BASE_SHA="$base" GOMAXPROCS=2 GOFLAGS='-p=2' "$gate_script" >"$output" 2>&1
	local status=$?
	set -e
	cat "$output"
	return "$status"
}

setup_fixture no-tests no-tests
if run_gate no-tests; then
	echo "mutation threshold regression: source without tests passed the 80% covered-MSI gate" >&2
	exit 1
fi
if ! grep -Fq 'Covered MSI 0.00% is below minimum required 80.00%' "$workspace/no-tests.log"; then
	echo "mutation threshold regression: no-test fixture did not fail the covered-MSI gate" >&2
	exit 1
fi
if ! cmp -s "$workspace/no-tests.expected" "$workspace/no-tests/positive.go"; then
	echo "mutation threshold regression: source was not restored after the failing gate run" >&2
	exit 1
fi
echo "mutation threshold regression: no-test project was rejected by covered-MSI"

setup_fixture weak-tests weak-tests
if run_gate weak-tests; then
	echo "mutation threshold regression: weak tests passed the 80% covered-MSI gate" >&2
	exit 1
fi
if ! grep -Fq 'The covered-code mutation score is' "$workspace/weak-tests.log" || ! grep -Fq 'is below minimum required 80.00%' "$workspace/weak-tests.log"; then
	echo "mutation threshold regression: weak-test fixture did not fail the covered-MSI gate" >&2
	exit 1
fi
if ! cmp -s "$workspace/weak-tests.expected" "$workspace/weak-tests/positive.go"; then
	echo "mutation threshold regression: source was not restored after the weak-test gate run" >&2
	exit 1
fi
echo "mutation threshold regression: weak tests were rejected by covered-MSI"

setup_fixture no-mutations no-mutations
if run_gate no-mutations; then
	echo "mutation threshold regression: no-mutation change passed the 80% covered-MSI gate" >&2
	exit 1
fi
if ! grep -Fq 'The mutation score is 0.00% (0 killed, 0 escaped, 0 errored, 0 not covered, 0 skipped, 0 total)' "$workspace/no-mutations.log" ||
	! grep -Fq 'The covered-code mutation score is 0.00%' "$workspace/no-mutations.log" ||
	! grep -Fq 'Covered MSI 0.00% is below minimum required 80.00%' "$workspace/no-mutations.log"; then
	echo "mutation threshold regression: no-mutation fixture did not fail the covered-MSI gate" >&2
	exit 1
fi
if ! cmp -s "$workspace/no-mutations.expected" "$workspace/no-mutations/positive.go"; then
	echo "mutation threshold regression: source was not restored after the no-mutation gate run" >&2
	exit 1
fi
echo "mutation threshold regression: no-mutation project was rejected by covered-MSI"

setup_fixture tested tested
if ! run_gate tested; then
	echo "mutation threshold regression: boundary tests did not pass the 80% covered-MSI gate" >&2
	exit 1
fi
if ! grep -Fq 'The covered-code mutation score is 100.00%' "$workspace/tested.log"; then
	echo "mutation threshold regression: tested fixture did not report its covered-MSI" >&2
	exit 1
fi
if ! cmp -s "$workspace/tested.expected" "$workspace/tested/positive.go"; then
	echo "mutation threshold regression: source was not restored after the passing gate run" >&2
	exit 1
fi
echo "mutation threshold regression: boundary tests passed the covered-MSI gate"
echo "mutation threshold regression: deleted files were skipped and source was restored"
