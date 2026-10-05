#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
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
	# A deleted file has nothing left to mutate.
	[[ -f "$file" ]] && files+=("$file")
done < <({ git diff --name-only "$base"...HEAD; git diff --cached --name-only; git diff --name-only; } | sort -u | sed -nE '/\.go$/p' | grep -vE '(^|/)[^/]*_test\.go$' || true)
if ((${#files[@]} == 0)); then
	echo "mutation threshold: no changed production Go files"
	exit 0
fi

config="$script_dir/../config/mutago.yml"
if [[ ! -f "$config" ]]; then
	echo "mutation threshold: mutago config not found: $config" >&2
	exit 1
fi

"$script_dir/run-mutago.sh" \
	--config="$config" \
	--coverage \
	--min-covered-msi=80 \
	--git-diff-lines \
	--git-diff-base="$base" \
	--workers=2 \
	--quiet \
	--no-diffs \
	"${files[@]}"
