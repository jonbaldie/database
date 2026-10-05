#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${MUTAGO_BIN:-}" ]]; then
	if [[ ! -x "$MUTAGO_BIN" ]]; then
		echo "mutation threshold: MUTAGO_BIN is not executable" >&2
		exit 1
	fi
	exec "$MUTAGO_BIN" "$@"
fi

go_command="${GO:-go}"
module="${MUTAGO_MODULE:-github.com/quality-gates/mutago/v2/cmd/mutago}"
version="${MUTAGO_VERSION:-v2.10.20}"
exec "$go_command" run "${module}@${version}" "$@"
