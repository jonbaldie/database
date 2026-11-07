---
name: verify-properties
description: Formal verification here is an independent-model property. Use for a formal-verification request, a Fuzz-target change, one fuzz campaign, or a counterexample replay. Public executable evidence uses verify-database.
---

# Verify properties

Formal verification in this repository is a property on a Go `Fuzz` target. The oracle is an independent model, a round trip, or a return. Read `GLOSSARY.md` and use its terms for product behaviour. The public SQL property compares query behaviour: exact rows after each action and after restart. That property creates the database namespace `cgpt`.

`verify-database` launches the executable and records public evidence: operator command results, MySQL wire results, and diagnostics. Use it for that evidence. Use this skill for the oracle and for a fuzz campaign.

## Select the property

Read the target that owns the behaviour you changed.

| Behaviour | Target | Oracle |
| --- | --- | --- |
| Committed storage rows, including after reopen | `FuzzStorageTransactionsMatchModel` in `internal/storage/storage_fuzz_test.go` | The row map matches `SnapshotRows`. A conflicting update fails and the map stays unchanged. |
| WAL payload round trip | `FuzzWALPayloadRoundTrip` in `internal/storage/wal_fuzz_test.go` | The decoded payload equals the encoded namespace, name, and two row values. |
| WAL payload decode | `FuzzWALPayloadDecodeNeverPanics` in `internal/storage/wal_fuzz_test.go` | `decodePayload` returns on arbitrary bytes. |
| Scalar evaluation | `FuzzEvaluateScalarDoesNotPanic` in `internal/mysql/expression_fuzz_test.go` | `evaluateScalar` returns on the input expression. |
| Public SQL query behaviour | `FuzzBlackBoxSQLStateful` in `test/blackbox/cgpt_stateful_test.go` | Exact rows match the model after each action and after restart. MySQL errors `1062` and `1048` leave the model unchanged. |

The public target drives a real executable. Record that attempt with `verify_command` from `verify-database`. The model and the campaign decision stay here. Read `docs/exploratory-testing/2026-09-16-cgpt.md` before you extend that target, then re-read the target file. While the file still matches that report, the property covers auto-commit insert, update, delete, duplicate key, and `NOT NULL` failure. Transactions, savepoints, grants, and backup need a new action and a new seed in that file before a campaign may claim them.

`TestStatefulSequenceCampaign` checks operator command results on scripted sequences. That is public executable evidence. Drive it with `verify-database`.

A new check is another `Fuzz` function in the package that owns the behaviour, using an independent model, a round trip, or a return.

These claims have other owners. Use the owner and stop this skill:

- Changed production lines and covered-code MSI: `make mutation` and `CODING_STANDARDS.md`.
- The production quality gate: `make quality`.
- The release audit of public tests: `docs/conformance-evidence.md`.
- Query explanation grammar and semantic rules: `make validate-query-explanation` and `docs/query-explanation/README.md`. Query explanation behaviour: `verify-database`.
- The v0.1 performance acceptance scenario: `make performance` and `docs/performance-acceptance.md`.

Done when you have named the target file and written its oracle in one sentence, or you have handed the claim to the owner above.

## Keep the model independent

Write the model from the action parameters. Read the system only for the comparison. Compare after every action. A rejected action leaves the model as it was. Choose existing keys from a sorted key list. Keep the input cap already written in the target. Add a seed with `f.Add` for each new path.

Done when every new success writes the model, every new rejection leaves the model unchanged, and `f.Add` contains a seed for that path.

## Replay the seeds

A seed run executes the `f.Add` inputs as an ordinary test. `make test` and `go test ./...` are seed runs. Apply the processor limits in `AGENTS.md` under Resource-safe local verification.

```sh
go test <package> -run '^<FuzzName>$' -count=1
```

Done when the exit status is 0 and the output names that target. On a failing seed, keep the output and continue at Replay a counterexample.

## Run one campaign

Run a campaign only after the seed run for that target exits 0. One package, one fuzz target, one campaign at a time. Use the command shape in `AGENTS.md`: `-run '^$'`, `-fuzz '^<FuzzName>$'`, and `-fuzztime=30s`. Replace the package and the target name. Apply the same processor limits.

Call the run a fuzz campaign when it uses `-fuzz` and `-fuzztime`. A seed run stays a seed run.

Done when the output names that one target and a fuzz duration. Record the package, the target, the duration, and whether a counterexample appeared.

## Replay a counterexample

Run the failing input as a seed with `-run '^<FuzzName>$'`. Shrink the sequence to the setup, the one failing action, and the comparison. Repair the product when it breaks the oracle you selected. Repair the model when the model encodes a different expected result from that oracle. Replay the seed until it exits 0, then run one campaign of that same target.

Done when the seed exits 0 and the following campaign reports no counterexample.

## Cleanup

Delete logs and data directories this run created. A `t.TempDir` directory belongs to the test process. Keep a `verify-database` evidence directory when that skill owns the attempt. Follow the cleanup rule in `AGENTS.md`.

Done when the retained generated files are only that evidence, and every generated artifact over 100 MB is reported with its path and size.
