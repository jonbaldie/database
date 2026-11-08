#!/bin/sh
# Model-check row-lock mutual exclusion and deadlock reports.
# TLC is tlaplus v1.7.4. The schedule test runs the same bounds on
# lockManager.waitAttempt, lockNowait, skipLocked, and release.
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
version=1.7.4
jar_sha=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88
tool_dir=$(mktemp -d "${TMPDIR:-/tmp}/row-locks.XXXXXX")
trap 'rm -rf "$tool_dir"' EXIT

curl -fsSL -o "$tool_dir/tla2tools.jar" \
  "https://github.com/tlaplus/tlaplus/releases/download/v${version}/tla2tools.jar"
printf '%s  %s\n' "$jar_sha" "$tool_dir/tla2tools.jar" | sha256sum -c -

cp "$root/internal/mysql/RowLocks.tla" "$tool_dir/RowLocks.tla"
grep -v -e '^INVARIANT JustifiedDeadlock$' -e '^INVARIANT NoLiveCycle$' \
  "$root/internal/mysql/RowLocks.cfg" > "$tool_dir/mutex.cfg"
cp "$root/internal/mysql/RowLocks.cfg" "$tool_dir/deadlock.cfg"
cd "$tool_dir"

echo "MutualExclusion"
java -Xmx512m -XX:+UseParallelGC -cp "$tool_dir/tla2tools.jar" tlc2.TLC \
  -workers 2 \
  -config mutex.cfg \
  RowLocks.tla

echo "JustifiedDeadlock and NoLiveCycle"
deadlock_status=0
java -Xmx512m -XX:+UseParallelGC -cp "$tool_dir/tla2tools.jar" tlc2.TLC \
  -workers 2 \
  -config deadlock.cfg \
  RowLocks.tla || deadlock_status=$?

cd "$root"
schedule_status=0
GOMAXPROCS=2 GOFLAGS='-p=2' go test -tags rowlockschedule ./internal/mysql \
  -run '^TestRowLockSchedule$' -count=1 -timeout 2m || schedule_status=$?

if [ "$deadlock_status" -ne 0 ] || [ "$schedule_status" -ne 0 ]; then
  exit 1
fi
