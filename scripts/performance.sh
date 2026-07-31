#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$ROOT/bin" "$ROOT/dist"
go test ./... >/dev/null
go build -trimpath -buildvcs=false -o "$ROOT/bin/database" ./cmd/database
go run ./scripts/performance \
  --executable "$ROOT/bin/database" \
  --output "$ROOT/dist/performance-evidence.json" \
  "$@"
