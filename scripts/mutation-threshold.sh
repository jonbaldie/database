#!/usr/bin/env bash
set -euo pipefail

# Mutation testing is deliberately changed-code scoped. The CI job checks out
# the complete history so this script can compare the PR with its merge base.
threshold="${MUTATION_THRESHOLD:-0.80}"
base="${GITHUB_BASE_SHA:-${GITHUB_BASE_REF:-origin/main}}"
if ! git rev-parse --verify "$base" >/dev/null 2>&1; then
	base="$(git merge-base HEAD origin/main 2>/dev/null || true)"
fi
if [[ -z "$base" ]]; then
	echo "mutation threshold: cannot determine the comparison base" >&2
	exit 1
fi

files=()
while IFS= read -r file; do
	[[ -n "$file" ]] && files+=("$file")
done < <({ git diff --name-only "$base"...HEAD; git diff --cached --name-only; git diff --name-only; } | sort -u | sed -nE '/\.go$/p' | grep -vE '(_test\.go|^$)' || true)
if ((${#files[@]} == 0)); then
	echo "mutation threshold: no changed production Go files"
	exit 0
fi

if ! command -v go-mutesting >/dev/null 2>&1; then
	echo "mutation threshold: install go-mutesting before running this gate" >&2
	exit 1
fi

packages=()
patterns=()
for file in "${files[@]}"; do
	directory="$(dirname "$file")"
	package="$(go list "./$directory")"
	packages+=("$package")
	while read -r function; do
		[[ -n "$function" ]] && patterns+=("$function")
	done < <(sed -nE 's/^func ([A-Za-z_][A-Za-z0-9_]*).*/\1/p' "$file")
done

unique_packages=()
while IFS= read -r package; do
	[[ -n "$package" ]] && unique_packages+=("$package")
done < <(printf '%s\n' "${packages[@]}" | sort -u)
packages=("${unique_packages[@]}")
if ((${#patterns[@]} == 0)); then
	echo "mutation threshold: changed files contain no mutatable functions"
	exit 0
fi
match="$(IFS='|'; echo "${patterns[*]}")"
output="$(mktemp)"
trap 'rm -f "$output"' EXIT
go-mutesting --match="$match" --exec='go test ./...' "${packages[@]}" 2>&1 | tee "$output"

score="$(sed -nE 's/.*mutation score is ([0-9.]+).*/\1/p' "$output" | tail -1)"
if [[ -z "$score" ]]; then
	echo "mutation threshold: mutation tool did not report a score" >&2
	exit 1
fi
awk -v score="$score" -v threshold="$threshold" 'BEGIN {
	if (score + 0 < threshold + 0) {
		printf "mutation threshold: %.2f is below required %.2f\n", score, threshold > "/dev/stderr"
		exit 1
	}
	printf "mutation threshold: %.2f meets required %.2f\n", score, threshold
}'
