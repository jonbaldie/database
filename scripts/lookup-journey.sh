#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
make build
exec go run ./scripts/lookup-journey -executable "$ROOT/bin/database" "$@"
