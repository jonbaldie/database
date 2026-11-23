# Query explanation

## Sub-features

- Planned JSON and table output.
- Completed SELECT runtime evidence.
- Active query inspection by connection identifier.

## How to get to it (user POV)

Use `EXPLAIN FORMAT=JSON SELECT ...`, `EXPLAIN ANALYZE FORMAT=JSON SELECT ...`, or `EXPLAIN FORMAT=JSON FOR CONNECTION <connection_id>` through a supported MySQL client. The contract is in `docs/query-explanation/README.md`.

## Driving it with Go process tests

Run from the repository root, in the shell where you completed [Launch](../SKILL.md#launch). Use `verify_command` for every attempt and retry.

```sh
verify_command explanation env GOMAXPROCS=2 GOFLAGS='-p=2' go test ./test/blackbox \
	-run '^(TestMySQLPlannedExplanationUsesTheWireContract|TestMySQLAnalyzeAndLiveExplanationUseTheWireContract)$' \
	-count=1 -v
```

The planned test checks the envelope, scan strategy, and table columns. The analyzed and live test checks completed runtime evidence, active progress, and observer permissions. Read `test/blackbox/query_explanation_test.go` for the exact values.

## Gotchas

`EXPLAIN ANALYZE` executes a non-locking SELECT. A planned explanation has no runtime evidence. Active evidence is partial. Keep those distinctions when checking results. Explain and ordinary query results must agree for the same SQL and data.
