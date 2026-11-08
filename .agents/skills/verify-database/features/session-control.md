# Session observation and control

## Sub-features

- Process list visibility by account and privilege.
- Query cancellation and connection termination.
- Session termination when an account is locked or dropped.

## How to get to it (user POV)

Use `SHOW PROCESSLIST`, `information_schema.PROCESSLIST`, or `KILL` through a supported MySQL client. Lock or drop an account with `ALTER USER` or `DROP USER`.

## Driving it with Go process tests

Run from the repository root, in the shell where you completed [Launch](../SKILL.md#launch). Use `verify_command` for every attempt and retry.

```sh
verify_command session-control env GOMAXPROCS=2 GOFLAGS='-p=2' go test ./test/blackbox \
	-run '^(TestMySQLSessionObservationAndQueryCancellation|TestIssue569LiveExplanationRespectsObservation|TestIssue569AccountTerminationWaitsForRunningStatement)$' \
	-count=1 -v
```

The tests check both process lists, account permissions, query cancellation, and transaction rollback after session termination.
