#!/usr/bin/env bash
set -euo pipefail

# Mutation testing is deliberately changed-code scoped. The CI job checks out
# the complete history so this script can compare the PR with its merge base.
mutago_version="${MUTAGO_VERSION:-v2.7.7}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
mutago_config="${MUTAGO_CONFIG:-$script_dir/../config/mutago.yml}"
threshold="${MUTATION_THRESHOLD:-80}"
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
	base="$(git merge-base HEAD origin/main 2>/dev/null || true)"
fi
if [[ -z "$base" ]]; then
	echo "mutation threshold: cannot determine the comparison base" >&2
	exit 1
fi

files=()
while IFS= read -r file; do
	[[ -n "$file" ]] && files+=("$file")
done < <({ git diff --name-only "$base"...HEAD; git diff --cached --name-only; git diff --name-only; } | sort -u | sed -nE '/\.go$/p' | grep -vE '(_test\.go|(^|/)testdata/|^$)' || true)
if ((${#files[@]} == 0)); then
	echo "mutation threshold: no changed production Go files"
	exit 0
fi

case "$threshold" in
	''|*[!0-9.]*)
		echo "mutation threshold: MUTATION_THRESHOLD must be a number" >&2
		exit 1
		;;
esac

# Mutago accepts percentage points. Accept the former 0.80 form while keeping
# the public gate value at 80%.
threshold="$(awk -v value="$threshold" 'BEGIN {
	if (value >= 0 && value <= 1) {
		printf "%.12g", value * 100
	} else {
		printf "%.12g", value
	}
}')"

go run "github.com/quality-gates/mutago/v2/cmd/mutago@${mutago_version}" \
	--noop \
	--config="$mutago_config" \
	--coverage \
	--git-diff-lines \
	--git-diff-base="$base" \
	--min-covered-msi="$threshold" \
	--logger-github \
	--quiet \
	--no-diffs \
	"${files[@]}"
