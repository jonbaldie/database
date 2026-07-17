#!/usr/bin/env sh
set -eu
start=$(date +%s)
go test ./... >/dev/null
end=$(date +%s)
printf '{"schema":"database.performance/v1","verification":"go test ./...","duration_seconds":%s}\n' "$((end-start))"
