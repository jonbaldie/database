# Lifecycle and diagnostics

## Sub-features

- Version reports.
- Initialization, readiness, and graceful stop.
- Public health and metrics endpoints.

## How to get to it (user POV)

Use `database version`, `database init`, and `database serve` from `README.md`. Use `/live`, `/ready`, and `/metrics` on the diagnostics listener.

## Driving it with Go process tests

Run from the repository root, in the shell where you completed [Launch](../SKILL.md#launch). Use `verify_command` for every attempt and retry.

```sh
verify_command lifecycle env GOMAXPROCS=2 GOFLAGS='-p=2' go test ./test/blackbox \
	-run '^(TestExecutableVersionAndLifecycleArePublic|TestDiagnosticsHTTPContractIsObservableEndToEnd)$' \
	-count=1 -v
```

`TestExecutableVersionAndLifecycleArePublic` checks the version, readiness event, public health response, and clean process exit. Read `test/blackbox/diagnostics_test.go` for the endpoint assertions.

## Gotchas

One live process owns each data directory. Use private data and separate ports. Initialization takes a password file or standard input, not an inline password flag. Public command and configuration contracts are in `docs/operator-automation.md` and `docs/server-configuration.md`.
