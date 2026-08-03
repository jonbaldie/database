#!/usr/bin/env bash
# control.sh drives one isolated `database` instance for verification.
# Every subcommand after `start` takes the run identifier that `start` printed,
# because shell state does not survive between agent tool calls.
#
#   control.sh start                      -> prints RUN=<id> and the addresses
#   control.sh doctor  <run>              -> read-only health and identity check
#   control.sh sql     <run> [--user U --password P] [--hold 5s] <statement>...
#   control.sh diag    <run> <path>       -> GET /live, /ready or /metrics
#   control.sh catalog <run>              -> print the on-disk catalog.json
#   control.sh log     <run>              -> print the serve lifecycle log
#   control.sh restart <run>              -> graceful stop, then serve again
#   control.sh stop    <run>              -> graceful stop, keep the data dir
#   control.sh clean   <run>              -> stop and delete the run directory
#
# Evidence is written outside the repository, under
# /tmp/verify-database-evidence/<run>/, and `clean` never touches it.
set -euo pipefail

# -P resolves the .claude/skills symlink, so both invocation paths land on the
# real .agents location and on the same Go package path.
HELPERS_DIR=$(cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -P -- "$HELPERS_DIR/../../../.." && pwd)
BINARY="$REPO_ROOT/bin/database"
SQLCLIENT_PACKAGE=".${HELPERS_DIR#"$REPO_ROOT"}/sqlclient"
RUN_ROOT=/tmp/verify-database
EVIDENCE_ROOT=/tmp/verify-database-evidence

die() { echo "control.sh: $*" >&2; exit 1; }

run_dir() {
  local run=${1:-}
  [ -n "$run" ] || die "missing run identifier; see 'control.sh start' output"
  local dir="$RUN_ROOT-$run"
  [ -d "$dir" ] || die "unknown run '$run' (no $dir)"
  echo "$dir"
}

load_run() {
  RUN_DIR=$(run_dir "${1:-}")
  # shellcheck disable=SC1091
  . "$RUN_DIR/instance.env"
}

free_port() {
  python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'
}

wait_ready() {
  local dir=$1 deadline=$((SECONDS + 30))
  while [ $SECONDS -lt $deadline ]; do
    if grep -q '"state":"ready"' "$dir/serve.log" 2>/dev/null; then return 0; fi
    kill -0 "$(cat "$dir/serve.pid")" 2>/dev/null || { cat "$dir/serve.log" >&2; die "serve exited before ready"; }
    sleep 0.2
  done
  cat "$dir/serve.log" >&2
  die "serve did not report ready within 30s"
}

start_process() {
  local dir=$1
  # shellcheck disable=SC1091
  . "$dir/instance.env"
  "$BINARY" serve \
    --data-directory "$DATA_DIR" \
    --mysql-listen-address="$MYSQL_ADDRESS" \
    --diagnostics-listen-address="$DIAGNOSTICS_ADDRESS" \
    --format=json >>"$dir/serve.log" 2>&1 &
  echo $! >"$dir/serve.pid"
  wait_ready "$dir"
}

stop_process() {
  local dir=$1 pid
  [ -f "$dir/serve.pid" ] || return 0
  pid=$(cat "$dir/serve.pid")
  # Only ever signal the PID this run started. Never match by process name.
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    local deadline=$((SECONDS + 15))
    while kill -0 "$pid" 2>/dev/null && [ $SECONDS -lt $deadline ]; do sleep 0.2; done
    kill -0 "$pid" 2>/dev/null && die "pid $pid ignored SIGTERM; inspect $dir/serve.log"
  fi
  rm -f "$dir/serve.pid"
}

command=${1:-}
shift || true

case "$command" in
start)
  [ -x "$BINARY" ] || (cd "$REPO_ROOT" && make build >/dev/null)
  RUN=$(date +%s)-$$
  DIR="$RUN_ROOT-$RUN"
  mkdir -p "$DIR" "$EVIDENCE_ROOT/$RUN"
  printf 'verify-secret-%s\n' "$RUN" >"$DIR/password"
  chmod 600 "$DIR/password"
  {
    echo "RUN=$RUN"
    echo "DATA_DIR=$DIR/data"
    echo "PASSWORD_FILE=$DIR/password"
    echo "MYSQL_ADDRESS=127.0.0.1:$(free_port)"
    echo "DIAGNOSTICS_ADDRESS=127.0.0.1:$(free_port)"
    echo "EVIDENCE_DIR=$EVIDENCE_ROOT/$RUN"
  } >"$DIR/instance.env"
  # shellcheck disable=SC1091
  . "$DIR/instance.env"
  "$BINARY" init "$DATA_DIR" --password-file "$PASSWORD_FILE" --format=json >"$DIR/init.json"
  start_process "$DIR"
  cat "$DIR/instance.env"
  ;;

doctor)
  load_run "${1:-}"
  pid=$(cat "$RUN_DIR/serve.pid" 2>/dev/null || echo "")
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null || die "no live serve process for run $RUN"
  echo "process: pid $pid alive"
  echo "version: $("$BINARY" version --format=json)"
  echo "owned:   $(grep -c '"state":"ready"' "$RUN_DIR/serve.log") ready event(s) in this run's log"
  echo "ready:   $(curl -fsS "http://$DIAGNOSTICS_ADDRESS/ready")"
  echo "live:    $(curl -fsS -o /dev/null -w '%{http_code}' "http://$DIAGNOSTICS_ADDRESS/live")"
  echo "lock:    $([ -f "$DATA_DIR/.running.lock" ] && echo present || echo MISSING)"
  auth=$(cd "$REPO_ROOT" && go run "$SQLCLIENT_PACKAGE" -address "$MYSQL_ADDRESS" -user admin -password-file "$PASSWORD_FILE" 'SELECT 1')
  echo "auth:    $auth"
  ;;

sql)
  load_run "${1:-}"; shift
  user=admin; credential=(-password-file "$PASSWORD_FILE"); options=()
  while [ "${1:-}" = "--user" ] || [ "${1:-}" = "--password" ] || [ "${1:-}" = "--hold" ]; do
    case $1 in
    --user) user=$2 ;;
    --password) credential=(-password "$2") ;;
    --hold) options+=(-hold "$2") ;;
    esac
    shift 2
  done
  cd "$REPO_ROOT"
  exec go run "$SQLCLIENT_PACKAGE" -address "$MYSQL_ADDRESS" -user "$user" "${credential[@]}" "${options[@]+"${options[@]}"}" "$@"
  ;;

diag)
  load_run "${1:-}"
  curl -fsS "http://$DIAGNOSTICS_ADDRESS/${2#/}"
  ;;

catalog)
  load_run "${1:-}"
  cat "$DATA_DIR/catalog.json"
  ;;

log)
  load_run "${1:-}"
  cat "$RUN_DIR/serve.log"
  ;;

restart)
  load_run "${1:-}"
  stop_process "$RUN_DIR"
  start_process "$RUN_DIR"
  echo "restarted run $RUN on $MYSQL_ADDRESS"
  ;;

stop)
  load_run "${1:-}"
  stop_process "$RUN_DIR"
  echo "stopped run $RUN; data directory $DATA_DIR kept"
  ;;

clean)
  load_run "${1:-}"
  stop_process "$RUN_DIR"
  rm -rf "$RUN_DIR"
  echo "removed $RUN_DIR; evidence kept in $EVIDENCE_DIR"
  ;;

*)
  sed -n '2,20p' "${BASH_SOURCE[0]}"
  exit 1
  ;;
esac
