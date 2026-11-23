#!/usr/bin/env bash
set -euo pipefail

# Compare changed production lines with the PR merge base, including local edits.
threshold="${MUTATION_THRESHOLD:-0.80}"
base="${GITHUB_BASE_SHA:-}"
if [[ -z "$base" ]]; then
	base_ref="${GITHUB_BASE_REF:-main}"
	for candidate in "origin/$base_ref" "$base_ref" origin/main; do
		if git rev-parse --verify "$candidate" >/dev/null 2>&1; then
			base="$candidate"
			break
		fi
	done
fi
if [[ -z "$base" ]]; then
	echo "mutation threshold: cannot determine the comparison base" >&2
	exit 1
fi
git rev-parse --verify "$base^{commit}" >/dev/null

files=()
while IFS= read -r file; do
	[[ -f "$file" ]] && files+=("$file")
done < <({ git diff --name-only "$base"...HEAD; git diff --cached --name-only; git diff --name-only; } | sort -u | sed -nE '/\.go$/p' | grep -vE '(_test\.go|^$)' || true)
if ((${#files[@]} == 0)); then
	echo "mutation threshold: no changed production Go files"
	exit 0
fi

if ! mutago_path="$(command -v mutago)"; then
	echo "mutation threshold: install mutago before running this gate" >&2
	exit 1
fi
if [[ -z "${MUTAGO_VERSION:-}" ]]; then
	echo "mutation threshold: MUTAGO_VERSION is not set; run make mutation" >&2
	exit 1
fi
# Only the pinned mutago build may report the covered-code MSI.
mutago_module=github.com/quality-gates/mutago/v2
if ! go version -m "$mutago_path" 2>/dev/null | awk -v module="$mutago_module" -v version="$MUTAGO_VERSION" '
	$1 == "mod" && $2 == module && $3 == version { found = 1 }
	END { exit !found }'; then
	echo "mutation threshold: $mutago_path is not $mutago_module $MUTAGO_VERSION" >&2
	exit 1
fi
minimum="$(awk -v threshold="$threshold" 'BEGIN {
	if (threshold !~ /^[0-9]+([.][0-9]+)?$/ || threshold < 0 || threshold > 1) exit 1
	print threshold * 100
}')" || { echo "mutation threshold: MUTATION_THRESHOLD must be between 0 and 1" >&2; exit 1; }

temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT
export GOCACHE="${GOCACHE:-$temporary_directory/cache}"
# Include code without tests. Keep existing reports intact.
printf 'skip_without_test: false\njson_output: false\n' > "$temporary_directory/config.yml"

mutago \
	--config "$temporary_directory/config.yml" \
	--workers=2 \
	--exec-timeout 10 \
	--timeout-coefficient 2 \
	--coverage \
	--git-diff-lines \
	--git-diff-base "$base" \
	--ignore-msi-with-no-mutations \
	--min-msi "$minimum" \
	--min-covered-msi "$minimum" \
	"${files[@]}"
