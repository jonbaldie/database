# Statement execution check results

Status: PASS within the bounds below. The combined runner checks 23 current models, 23 broken controls, and 26 source faults. No current-source defect was found. Negative controls are intentional failures.

Source sync commit: `fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1`.
Check date: 2026-11-09. The skill does not change product files.

## Commands and results

Run from the repository root:

```sh
.agents/skills/verify-statement-execution/run
```

Exit status: 0. The runner sets `GOMAXPROCS=2` and `GOFLAGS=-p=2`. It runs TLC with one worker and two active processors. The jar is checksum pinned in the runner. Current Go probes passed before and after the eleven initial source mutations and fifteen sequence source mutations. Each mutation failed at the required behavioral assertion. None was counted from a build error.

The skill frontmatter check returned `Skill is valid!`. Both Go probe files were formatted with gofmt. The final combined runner exited 0. The REPLACE broken model was then aligned to the actual wrong-candidate source fault (candidate 1 reused for candidate 2); its current and broken chart checks again returned 0 and 1. All generated statechart commands completed: current models returned 0; broken controls returned 1 with their expected rule violation. Every chart remains under the 500-state budget.

| History | Model | Current distinct states | Rule that holds; broken control violates it |
| --- | --- | --- | --- |
| 22 | Resources | 9 | ResourceAccounting: aggregate use equals held reservations. |
| 26 | ResultOwnership | 5 | ReleaseOnce: close releases a result at most once. |
| 27 | SpillMerge | 4 | SortedRows: merging two sorted runs emits the expected order. |
| 28 | LimitDelivery | 48 | ExactSlice: output is the independent offset/count slice. |
| 29 | CTEReuse | 3 | BuildOnce: two references share one build. |
| 30 | RollingWindow | 5 | FrameCount: each single-row frame counts one row. |
| 31 | OuterJoin | 5 | PreserveUnmatchedLeft: unmatched left rows survive. |
| 32 | SetMultiplicity | 40 | IntersectionCount: output count is the lesser input count. |
| 33 | MutationFinish | 2 | RejectedAutocommitClearsStage: failed finish clears the staged transaction. |
| 34 | RangeSearch | 12 | InclusiveLowerBound: search returns the first eligible index. |

The skill table gives each bound and source boundary. Model the Domain led to ten separate models so that an ownership proof cannot stand in for a join or mutation proof. Prove It Works required matching source probes and intentional source faults.

## Source faults caught

| Overlay fault | Named assertion that failed |
| --- | --- |
| Omit aggregate memory release | closed statements retained resources |
| Retain result release callback after close | result released more than once |
| Omit materialized result close | materialization retained result ownership |
| Skip an extra streamed offset row | streamed limit slice changed |
| Omit shared CTE cache publication | CTE aliases repeated materialization |
| Retain the removed window row count | rolling window retained an old row |
| Drop the unmatched left join row | left join lost unmatched row |
| Do not consume a set match | set multiplicity changed |
| Change inclusive lower bound to exclusive | ordered range boundary changed |
| Omit failed autocommit abort cleanup | rejected autocommit retained staged changes |
| Reverse the spill comparator | spill sort order changed |

The runner uses Go overlays in a temporary directory. It checks that each source anchor occurs once. It deletes its overlays and TLC working state on exit.

## Charts

Current charts and reports are in `models/<lowercase-model>/`. Broken-control charts and reports are in `controls/<lowercase-model>/`. Each report gives its shortest bug path. Each broken report names the same rule as the table above. The paths reproduce with the source fault associated with that boundary. They do not reproduce against current source.

The charts combine model states with the same reader-facing label. Steps within one label do not appear as arrows. No view returns null to hide extra steps. The chart is an abstraction of the model; the TLA+ model and report retain the detailed checks.

Use `/statechart` to regenerate a chart. Set STATECHART to the installed statechart command. This example creates a new folder outside the repository, copies only the review artifacts, then removes the temporary folder:

```sh
skill=.agents/skills/verify-statement-execution
scratch=$(mktemp -d /tmp/database-statement-chart.XXXXXX)
"$STATECHART" \
  "$PWD/$skill/ResultOwnership.tla" \
  --config "$PWD/$skill/ResultOwnership.cfg" \
  --view "$PWD/$skill/ResultOwnershipView.js" --out "$scratch/current"
cp "$scratch/current/chart.mmd" "$scratch/current/report.md" \
  docs/formal/statement-execution/models/resultownership/
rm -rf "$scratch"
```

Repeat for each model. Use its Broken.cfg for the control and copy to its controls folder. A control exits 1 because it breaks the intended rule. Temporary statechart files are not stored in this repository.

## Sequence expansion

Thirteen additional models now cover ordered stages of histories 22 and 27 through 34. Their exact finite bounds and source functions are in the skill's Ordered sequence checks table. Current charts and reports are in `sequences/<lowercase-model>/`; expected broken paths are in `sequence-controls/<lowercase-model>/`.

| Model | Current distinct states | Rule checked |
| --- | --- | --- |
| AlterPublication | 9 | ValidateBeforePublication |
| IndexBoundConstruction | 61 | BoundsMatchPredicates |
| JoinSequence | 7 | MatchThenPreserveLeft |
| MembershipSequence | 12 | NullAwareMembership |
| MutationSequence | 9 | OrderedCandidates |
| ResourceFinalization | 11 | PublishedWorkStaysSuccessful |
| ScalarSequence | 11 | ScalarCardinality |
| SetFoldSequence | 3 | IntersectBeforeUnion |
| SkipLockedSequence | 4 | AvailableOffsetOnly |
| SpillLifecycle | 5 | DeliveredAndReleased |
| SubqueryScopes | 10 | ScopeControlsReuse |
| UpdateSequence | 6 | AssignmentsSeePriorValues |
| WindowSequence | 7 | PeerRankAndFrame |

The sequence controls replay small source faults through real functions or SQL. All fifteen must fail the stated behavior: working-row UPDATE assignments, three-term set precedence, dependent subquery scope, ordered REPLACE, rejected ALTER publication, inclusive/exclusive lower and upper tightening, finalization before late cancellation, scalar cardinality, peer rank, unmatched joins, null-aware membership, spill cleanup, failed SELECT lock restoration, and available-row offset counting. The dependent cache fault makes the correlated SQL fail with unknown outer column; the probe requires that query to succeed. Build errors are never accepted.

| Source fault | Named assertion |
| --- | --- |
| update-order | update assignment did not read prior assignment |
| set-precedence | intersection did not precede union |
| subquery-scope | dependent membership reused another scope |
| replace-order | ordered replacement did not retain last candidate |
| alter-validation | invalid altered schema was not rejected |
| lower-tightening | tightened bounds changed predicate intersection |
| upper-tightening | tightened bounds changed predicate intersection |
| finalization | published work rejected a late signal |
| scalar-cardinality | scalar accepted a second row |
| peer-rank | window partition peer rank or frame changed |
| join-sequence | join probe lost matched or unmatched output |
| membership-null | null-aware membership changed |
| spill-cleanup | spill lifecycle left a run file |
| restore-selection | failed selection retained a newly acquired lock |
| skip-offset | skip-locked offset counted an unavailable row |

The failed-selection probe is the source refinement for the transaction skill's LockCleanup model: hold row 1, acquire row 2, fail on row 3, then show row 2 is free and row 1 remains held. Its source control removes the caller restore operation.

Model the Domain made each ordered phase visible; a boundary helper cannot stand in for a complete sequence. Prove It Works required real calls and a source fault for each new property. The initial models remain useful checks for particular algorithms and ownership rules.

## Explicit limits

This checks finite sequences and examples in each assigned history. Formal bounds are in the skill. It does not prove arbitrary SQL execution. Generated Go range, limit, and multiplicity cases are broader than the models.

Resource release and result close are sequential. Concurrent release, cancellation races, arbitrary expressions, collations, null ordering, all window frames and aggregates, multiple set keys, right joins, recursive CTEs, foreign-key REPLACE delete phases, arbitrary mutation plans, and every disk failure path remain unchecked. Spill lifecycle assumes ordered runs and successful merge; SpillMerge checks one bounded merge. Its source probes force three-row spill and exercise complete and stopped delivery. Scalar callback release and partition reset have real probes; the corresponding models abstract those details.

MutationFinish checks the finish/abort helper boundary. The new SQL probes also check ordinary REPLACE, UPDATE, and ALTER publication, but do not prove every SQL path reaches the modeled validation boundary.
