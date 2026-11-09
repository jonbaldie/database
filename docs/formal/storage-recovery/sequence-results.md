# Storage sequence results

Source baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Product source has no changes from this work.

The combined command .agents/skills/verify-storage-recovery/run passed with exit 0. It includes the previous commit, WAL, checkpoint, schema-version, hydration, revision-claim, cache, and allocation checks. It now also runs run-sequences.

| Extended history | Current model | Distinct states | Rule |
| --- | --- | --- | --- |
| 9 | TableSchema | 34 | SchemaSuccessHasDurableImage |
| 11 | CatalogPublication | 14 | OutcomeMatchesPublication |
| 13 | AllocationLifecycle | 31 | AllocationLifecycleSafe |

The three current models passed. Nine broken configs failed their intended invariant. Current source probes in storage, catalog, and mysql passed before and after all nine source mutations. Every mutation failed a named behavioral assertion. The runner rejects build errors as evidence.

| Source mutation | Observed assertion |
| --- | --- |
| Omit metadata persistence from EnsureTable | successful schema change was not persisted |
| Omit the drop checkpoint | dropped rows reappeared after table recreation |
| Omit prepared row commit | catalog batch skipped durable row publication |
| Omit catalog schema persistence | catalog schema was not persisted before success |
| Notify waiters before commitWriteBatch | catalog notification preceded row publication |
| Send success after catalog failure | failed catalog publication returned success |
| Keep allocation state on a disabled table | disabled table retained allocation state |
| Ignore stored identifiers during initialization | initialization did not scan stored identifiers |
| Omit allocator reset | truncate did not reset exhausted allocation |

## What the source probes do

The schema probe starts with an absent table. It creates the table, commits one row, adds a column and unique index, checks the rebuilt primary lookup, repeats the same shape without a version change, checkpoints, and drops the table. Another probe creates a table, commits an old row, reshapes it, drops and recreates the name, restarts, and checks that the recreated table is empty. The error probe blocks rows/tables.meta.tmp with a directory. EnsureTable returns an error after its memory change. The old metadata file remains unchanged. The model therefore allows memory and disk to differ after a failed metadata operation.

The catalog probe queues two real write requests, closes the input queue, and runs writeLoop. The collected batch creates one table with two rows. A wrapper around the real row engine observes commit and checkpoint calls. No notification or new visible revision can exist when row commit begins. After the writer returns, both callers receive success, one revision is published, schema exists on disk, and a reopened catalog contains both rows. Separate apply, validator, and schema-path failures return errors and preserve the old visible revision. A schema-path failure occurs after row sync, so the row engine can contain the new rows while the visible catalog is old.

The allocator probe checks disabled state, empty initialization, scanning mixed identifiers, unsigned tinyint exhaustion, and retention of initialized state. It then executes CREATE, INSERT of 255, TRUNCATE, INSERT DEFAULT, and SELECT through the real SQL executor. The selected identifier is one. The test fixture only creates the catalog and executor.

## Bounds and assumptions

TableSchema uses one table and the fixed create, reshape, drop, recreate sequence. It separates metadata write, file sync, rename, directory sync, and drop checkpoint. Errors can terminate a metadata operation. It does not model all schema conversions, all error sites, arbitrary table sets, or operating-system power loss.

CatalogPublication uses one serialized writer and one batch. One abstract accepted change follows queue collection, application, validation, row preparation, row sync, row-schema checkpoint, schema persistence, revision publication, and waiter notification. An all-rejected path does not commit. Failure is possible during validation, row commit, or schema persistence. Arbitrary callbacks, concurrent scheduling, timer behavior, partial commits across many tables, and recovery from a row/schema mismatch remain outside the model. Row and schema persistence are separate steps, not an atomic transaction.

AllocationLifecycle uses disabled or uninitialized allocation, stored maximum zero through three, and at most three generated-value, explicit-value, or truncate steps. Scanning is one abstract step. The real scan includes zero rows, four mixed values, and the tinyint maximum. Concurrent allocation, all numeric types, arbitrary table conversion, and persistent restart of the SQL allocator remain outside this extension.

These checks extend the original histories at finite bounds. They do not remove the limits in the existing models.

## Charts and maintenance

The current charts and reports are in sequences/tableschema, sequences/catalogpublication, and sequences/allocationlifecycle. Each broken config has a checked chart/report pair in sequence-controls. All twelve chart commands returned the expected status. No report has an unchecked rule. The official skill validator passed with PyYAML supplied by uv.

The table chart has 12 labels and 18 arrows. It omits nine same-label steps. It merges equal metadata stages across operations; paths through merged labels can combine different operations, so read the model for exact operation order. The catalog chart has 11 labels and 14 arrows, with three same-label steps omitted. The allocator chart has five labels and seven arrows, with 44 same-label steps omitted. No view explicitly hides a cross-label transition.

Use /statechart with the matching model, config, and view in a new scratch directory outside the repo. Keep only chart.mmd and report.md. Recheck the source functions and sync commit before changing a covered transition. Use /maintain-formal-verification-skill when covered source changes without these checks.

Model the Domain led to explicit metadata, publication, and allocator phases. Prove It Works led to source sequence probes, named fault assertions, and restored-current runs.
