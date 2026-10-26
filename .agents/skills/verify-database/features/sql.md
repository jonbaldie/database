# SQL and indexes

## Sub-features

- Authentication and session state.
- SQL reads and durable writes.
- Sort-spill cleanup after result metadata delivery fails.
- Index DDL, hints, bounded scans, and uniqueness.

## How to get to it (user POV)

Connect a supported MySQL client to the address from `database serve`. Use the Go client trial in `README.md`. The executable does not contain a SQL client.

## Driving it with Go process tests

Run from the repository root, in the shell where you completed [Launch](../SKILL.md#launch). Use `verify_command` for every attempt and retry.

```sh
verify_command sql env GOMAXPROCS=2 GOFLAGS='-p=2' go test ./test/blackbox \
	-run '^(TestGoDriverCompatibilityProfile|TestMySQLClientCanAuthenticatePersistAndResetSession|TestMySQLBTreeIndexesUseThePublicWireContract)$' \
	-count=1 -v
```

For sort-spill ownership and metadata failure, also run:

```sh
verify_command spill-cleanup env GOMAXPROCS=2 GOFLAGS='-p=2' go test ./test/blackbox \
	-run '^(TestMetadataFailureRemovesSortSpills|TestMySQLBoundsOrderedReadsAndPublishesResourceEvidence)$' \
	-count=1 -v
```

`TestMetadataFailureRemovesSortSpills` uses a private temporary directory. It
checks a failed text result after the column-count packet, positive spill
metrics, no remaining sort files, and a successful new connection. Binary
metadata and output failures use real spill results in `internal/mysql` tests;
they are not process-level prepared-execution proofs.

The index test checks literal rows from forward, reverse, and bounded scans. It also checks duplicate-key errors and public index metadata. The client state test checks persisted rows after restart. The Go driver test proves the named driver connection path.

## Gotchas

Use the supported subset in `docs/mysql-sql-behaviour.md`. Driver coverage is finite. Read `docs/compatibility-evidence.md` before claiming another driver's behavior. An in-process executor test alone does not prove authentication or wire encoding.
