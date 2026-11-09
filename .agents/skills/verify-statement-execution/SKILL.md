---
name: verify-statement-execution
description: Check bounded statement resource accounting, result ownership, sorting, limits, CTE reuse, rolling windows, joins, set multiplicity, failed statement cleanup, and ordered range boundaries when changing the covered MySQL execution code.
---

# Verify statement execution

Run from the repository root.

```sh
.agents/skills/verify-statement-execution/run
```

The command needs Python 3, Java 11 or later, and the Go toolchain in go.mod. Run verify-wal-atomicity once to install the checksum-pinned TLC 1.7.4 jar. TLA2TOOLS can select the same jar elsewhere. Go and TLC use at most two processors.

Read each current and broken model result with `/statechart`. Each model has a same-named config and a View.js file. Broken configs are negative controls. Their violations are expected and are not current-source defects. Read the chart reports in docs/formal/statement-execution.

## Source and sync

Last source review is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Inspect later commits and uncommitted changes in the listed functions before using this skill. Move the sync commit forward only after the models and probes match the changed code.

| History | Model and rule | Source boundary | Bound and probe |
| --- | --- | --- | --- |
| 22 | Resources, ResourceAccounting | resources.go, reserveStatementMemory, reserveStatementTemporary, closeStatementResources | Two statements reserve one abstract unit each, then close. Go checks sizes 0 through 2 under aggregate limit 3, both memory and temporary storage. |
| 26 | ResultOwnership, ReleaseOnce | server.go, queryResult.close; composed_select.go, materializeQueryResult | One result, two closes, success or failed delivery. Go checks copied rows, retained errors, automatic close, and repeated close. |
| 27 | SpillMerge, SortedRows | relational_spill.go, relationalSpillHeap.Less, relationalSpillSorter.less and streamRuns | Two sorted runs with values 1,3 and 2. Go forces sort spill with three named rows, then checks order and file and budget cleanup. |
| 28 | LimitDelivery, ExactSlice | relational_select.go, incrementalRows and yieldSourceRow; relational_select_shape.go, limitRelationalRows | Three model input rows, offsets and counts 0 through 3. Go checks buffered input sizes 0 through 4 with offsets and counts 0 through 5, and streamed three-row SQL with offsets and counts 0 through 4. |
| 29 | CTEReuse, BuildOnce | composed_select.go, materializeCTE and composedRelationState | Two aliases share one materialized result. Go passes the same original relation value twice and checks result identity. |
| 30 | RollingWindow, FrameCount | relational_select_window.go, rollingWindowAggregate.advanceActive and remove | Three consecutive one-row frames. Go checks COUNT over current-row frames for one through four input rows. |
| 31 | OuterJoin, PreserveUnmatchedLeft | relational_select_source.go, hashJoinRun.yieldLeftRow and yieldJoinedRows | Two left rows, one right match. Go checks a three-row left SQL join with one unmatched row. |
| 32 | SetMultiplicity, IntersectionCount | composed_select.go, includeSetRow | One row key, each input multiplicity 0 through 3, INTERSECT ALL. Go checks input multiplicities 0 through 4 for ALL and distinct intersection and difference. |
| 33 | MutationFinish, RejectedAutocommitClearsStage | transaction.go, statementTransaction.finish and abort | One staged transaction and a failed autocommit finish. Go stages SQL under BEGIN, then calls the finish helper with an injected failure and autocommit flag. |
| 34 | RangeSearch, InclusiveLowerBound | relational_select_source.go, ascendingOrderedIndexBounds and descendingOrderedIndexBounds | Binary search on three ordered integer keys for an inclusive lower bound. Go checks lengths 0 through 4, boundaries -1 through 5, ascending and descending order, and each inclusive and exclusive lower and upper bound. |

Paths in the table are under internal/mysql. Each model action represents a completed helper operation or loop step in the named source. Resource release and result close are sequential abstractions. They do not establish safety during concurrent calls.

## Expected checks

All current models and source probes must pass. Each broken model must fail its named rule. Eleven source overlays must fail at their named behavioral assertion.

- Resource close omits aggregate memory release.
- Result close retains its release callback.
- Materialization omits result close.
- Streamed limit skips one extra offset row.
- CTE materialization omits the shared cached result.
- Rolling removal retains an old row count.
- The hash left join omits its unmatched row.
- Set intersection does not consume a matched occurrence.
- The inclusive lower range bound becomes exclusive.
- Autocommit abort omits transaction cleanup.
- Spill sort reverses its comparison.

The runner uses temporary Go overlays. It does not change production files. Compilation errors and unrelated failures do not count as caught mutations. If a source anchor changes, inspect the source rather than changing the runner to bypass that check.

## Ordered sequence checks

The runner also calls `run-sequences`. It checks thirteen additional current and broken models and fifteen source overlays. Each overlay must fail the named behavioral assertion; then all current sequence probes run again.

| History | Sequence model and rule | Source stages and finite bound |
| --- | --- | --- |
| 22 | ResourceFinalization, PublishedWorkStaysSuccessful | resources.go: admission, reserve, precommit failure or finalization, late cancel/deadline, check, close. One statement and one reserved unit. |
| 27 | SpillLifecycle, DeliveredAndReleased | relational_spill.go: buffer, create two runs, merge three rows, deliver, close. Go checks full delivery and caller stop, files and disk budget. SpillMerge checks the merge ordering separately. |
| 28 | SkipLockedSequence, AvailableOffsetOnly | relational_select.go: scan a locked row, count one available offset row, acquire and return the next row. Go uses two real transactions and three rows. LimitDelivery covers ordinary slicing. |
| 29 | SubqueryScopes, ScopeControlsReuse; ScalarSequence, ScalarCardinality | composed_select.go: compile/cache independent input, evaluate two outer scopes; scalar reads zero through two rows, finalizes and releases. Go checks correlated SQL and buffered/streamed scalar results. CTEReuse checks shared CTE identity. |
| 30 | WindowSequence, PeerRankAndFrame | relational_select_window.go: ordered peers 1,1,2, position/rank/frame calculation, emit each row. SQL adds a second partition and checks reset, RANK, DENSE_RANK, and current-row SUM. RollingWindow checks frame removal. |
| 31 | JoinSequence, MatchThenPreserveLeft | relational_select_source.go: build right, probe two left rows, emit match or null row. Real SQL includes matched and unmatched rows. |
| 32 | MembershipSequence, NullAwareMembership; SetFoldSequence, IntersectBeforeUnion | composed_select.go: scan null/match/other members; evaluate three set terms, reduce INTERSECT, then fold UNION. Real membership and three-term set SQL accompany independent expected values. SetMultiplicity covers occurrence consumption. |
| 33 | MutationSequence, OrderedCandidates; UpdateSequence, AssignmentsSeePriorValues; AlterPublication, ValidateBeforePublication | server.go: ordered conflict deletion/insertion for two same-key REPLACE candidates; select, first UPDATE assignment, second assignment reading the first, validate, publish. ddl.go and index.go: clone, validate/apply private ALTER actions, validate indexes, publish or reject. SQL checks last replacement, assignment dependence, rejected multi-action ALTER leaves catalog unchanged, and successful ordered ALTER. |
| 34 | IndexBoundConstruction, BoundsMatchPredicates | relational_select_source.go: apply two predicates in order, retain stronger lower/upper bounds and exclusive equality. Model values 0 through 2, all five comparison operators. Go checks 225 predicate pairs against direct predicate intersection and SQL with a reversed operand. RangeSearch checks the subsequent search. |

History 18 delegates a real caller check here: FailedLockSelection begins with row 1 held, acquires row 2, then fails NOWAIT on row 3. A third session can acquire row 2 but cannot acquire row 1. The source control removes executeLockingRelationalSelect's restore call. The transaction subsystem owns the LockCleanup model.

The fifteen new source controls change UPDATE working-row visibility, set precedence, dependent cache scope, REPLACE candidate order, ALTER action rejection, lower and upper equality tightening, post-finalization cancellation, scalar cardinality, peer rank, unmatched join output, null membership, spill file release, failed selection restore, and SKIP LOCKED offset counting.

## Limits and maintenance

These are finite sequence checks plus source probes, not a proof of arbitrary SQL execution. The new sequence models cover the ordered stages listed above with tiny data. Arbitrary expressions, collations, null ordering, general window frames and aggregates, multi-key set rows, right joins, recursive CTEs, foreign-key REPLACE delete phases, arbitrary mutation plans, crashes at every I/O boundary, concurrent resource release, and cancellation races remain outside these models. SpillLifecycle assumes ordered runs and successful merge; SpillMerge separately checks a bounded merge. The scalar model abstracts callback release, while the probe checks real ownership. The window model assumes sorted peers; its SQL probe checks partition reset as well. The ALTER model abstracts each action and validation; its source probe checks one valid and one invalid action sequence. Source probes are examples, not exhaustive fault coverage.

MutationFinish checks cleanup through the finish helper directly. It does not show every statement reaches that helper. The new REPLACE, UPDATE, and ALTER probes also exercise normal SQL publication.

Keep current failures visible. Reproduce model violations against source before reporting a product defect. If behavior changes intentionally, update the model and probe together. Demonstrate each broken source check, then rerun this skill and `/statechart`. Use `/maintain-formal-verification-skill` after covered code changes without these checks.
