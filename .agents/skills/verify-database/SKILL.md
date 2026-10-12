---
name: verify-database
description: Verify database through its operator CLI, MySQL wire protocol, and HTTP diagnostics. Use before claiming user-visible behavior works or when selecting process tests for a change.
---

# Verify database

Read [the feature map](features/README.md). Select every affected entry point before the run.

## Launch

Run from the repository root. Create an evidence directory that you own and define `verify_command` in the current shell. Use it for each command, including retries.

```sh
evidence="$(mktemp -d "${TMPDIR:-/tmp}/database-verification.XXXXXX")"
verify_command() (
	label="$1"
	shift
	attempt="$(mktemp -d "$evidence/$label.XXXXXX")" || exit
	printf '%s\n' "$@" > "$attempt/command.txt" || exit
	if "$@" > "$attempt/output.log" 2>&1; then
		status=0
	else
		status=$?
	fi
	printf '%s\n' "$status" > "$attempt/exit-status.txt" || exit
	printf 'Evidence: %s\n' "$attempt" >&2
	exit "$status"
)
verify_command build env GOMAXPROCS=2 GOFLAGS='-p=2' make build
```

Each call creates a new attempt directory under `$evidence`, even when its label is reused. `command.txt` records one command argument per line. `output.log` keeps stdout and stderr. `exit-status.txt` keeps the command exit status, and the function returns that status. A missing exit-status file means the attempt did not finish recording its result.

The process tests build the executable, initialize private data directories, and start their own servers. They wait for lifecycle readiness before they send requests. Use those tests rather than a shared server.

For a manual server or a new client journey, follow `README.md` under `Try it`. Use a new data directory and unused loopback ports. A successful `/ready` response and an authenticated client ping prove readiness. Stop only the process you started with `SIGTERM`, then wait for its exit.

## Doctor

Check the built executable before you drive it.

```sh
verify_command version ./bin/database version --format=json
```

Check the command exit status and the reported product version. If a process test fails, inspect its output before another attempt. For a manual server, check the owned process, `/ready`, and an authenticated client ping.

## Drive

Run the exact tests named by the affected feature files. Use an uncached run and keep its output.

```sh
verify_command lifecycle env GOMAXPROCS=2 GOFLAGS='-p=2' go test ./test/blackbox \
	-run '^TestExecutableVersionAndLifecycleArePublic$' -count=1 -v
```

A passing test must observe the built server through commands, TCP, or HTTP. If no mapped test covers the changed entry point, add a process test for that path before declaring it verified.

## Evidence

Keep the action, result, and command exit status. A query proof checks exact returned values. A durable write proof checks the state after restart. A diagnostics proof checks the response code and body. Compilation alone is not evidence of user-visible behavior.

The commands above keep evidence in `$evidence`. Keep the same evidence directory and use `verify_command` for later commands and retries. Report each attempt path, its exit status, and the affected test names. Retain failed attempts as well as successful attempts. Use mocks only outside an existing production boundary.

## Cleanup

Process tests remove their private data directories and stop the processes they start. Wait for the test command to finish. Remove any additional scratch data or binaries you created, while preserving `$evidence`. Keep pre-existing data, servers, and caches unchanged.

## Helpers

Use `test/blackbox` and its process tests. No separate helper or global MySQL client is required. To update the feature map after a product change, use `maintain-verification-skill` when that skill is available.
