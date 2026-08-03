# Durable storage architecture and seams for replacing whole-catalog JSON

Research date: 2026-08-03

Primary sources: repository Go packages under `internal/`, `docs/performance-acceptance.md`, GitHub issues #4, #9, #16, #47, #53, #71, and `CONTEXT.md`. No ADRs exist yet (`docs/adr/` is absent).

## Verdict

Application rows today live inside one indented `catalog.json` as `[][]string`, and every durable commit rewrites that whole file after deep-cloning the catalog. There is no page store, WAL, B-tree page code, or LSM in the repo. “B-tree” is logical index metadata plus planner/explanation naming; physical access still materializes and sorts every table row in memory. Passing performance acceptance (#71) requires deepening `catalog.Store` into a real row-oriented engine behind the existing Snapshot/Apply/ReplaceIfRevision seam—not greenfielding a second SQL path through unused `internal/engine`.

## 1. How application table rows are stored today

### On-disk layout

An initialized data directory contains at least:

| Path | Owner | Role |
| --- | --- | --- |
| `instance.json` | `internal/instance` | Instance identity / lifecycle metadata |
| `catalog.json` | `internal/catalog` | Namespaces, tables, constraints, indexes, accounts, **and all table rows** |

`catalog.Open` reads `catalog.json` with `json.Unmarshal` into an in-memory `Definition`.[source](../../internal/catalog/catalog.go)

`Table.Rows` is the durable row image:

```37:45:internal/catalog/catalog.go
type Table struct {
	Name             string            `json:"name,omitempty"`
	Columns          []string          `json:"columns"`
	ColumnTypes      []string          `json:"column_types,omitempty"`
	ColumnAttributes []ColumnAttribute `json:"column_attributes,omitempty"`
	Constraints      []Constraint      `json:"constraints,omitempty"`
	Indexes          []Index           `json:"indexes,omitempty"`
	Rows             [][]string        `json:"rows,omitempty"`
}
```

Persistence path on every mutation:

1. `cloneDefinition` (deep-copies every namespace, table, and row)
2. validate
3. `json.MarshalIndent` of the **entire** definition
4. write `.catalog-*.tmp`, `Sync`, rename over `catalog.json`, sync the directory

[`persistLocked` / `writeCatalogTemp`](../../internal/catalog/catalog.go)

`Recover` only deletes abandoned `.catalog-*.tmp` files; there is no redo log.[source](../../internal/catalog/catalog.go)

### Runtime path used by SQL

`internal/lifecycle` opens the catalog and passes `*catalog.Store` into the MySQL server.[source](../../internal/lifecycle/server.go)

Almost all DML goes through `session.mutateCatalog` → either:

- autocommit: `Apply` + `ReplaceIfRevision`, or
- explicit txn: stage into `transactionSnapshot`, then `ReplaceIfRevision` on COMMIT

[source](../../internal/mysql/transaction.go)

Row mutations are whole-table rewrites of `table.Rows` via `mutateTableRows` in `internal/mysql/server.go` (clone rows, transform, assign back).

### Indexes today

`catalog.Index` / constraints are schema metadata only. Execution does **not** probe a physical index:

```657:676:internal/mysql/relational_select_source.go
func indexScanRows(table relationalTableSource) ([]indexedRelationRow, error) {
	if table.access == nil {
		// ... copy every table.Rows entry ...
	}
	rows := make([]indexedRelationRow, len(table.table.Rows))
	for number, values := range table.table.Rows {
		keys, err := indexScanKeys(table, *table.access, values)
		// ...
	}
	sort.SliceStable(rows, /* by index keys */)
	return rows, nil
}
```

`chooseTableIndexAccess` only picks which logical index to **claim** for explanations/hints; the scan still walks all rows.[source](../../internal/mysql/relational_select.go)

Constraint enforcement also linear-scans `table.Rows`.[source](../../internal/mysql/constraint_enforcement.go)

## 2. Existing row / page / WAL / B-tree / LSM code?

| Area | Finding |
| --- | --- |
| `internal/storage` | **Does not exist** |
| WAL / write-ahead / redo | None (only temp-file rename durability for whole catalog) |
| Page store / buffer pool | None |
| On-disk B-tree / LSM / SST | None; no pebble/badger/bolt/leveldb deps in `go.mod` |
| `internal/engine` | Standalone in-memory toy SQL (`[][]string` tables). **Not imported** by serve/lifecycle/mysql. Not a storage seam. |
| “btree_*” operators | Query-explanation vocabulary and planner strategy names only (`internal/queryexplanation`, `docs/query-explanation`) |
| Spill files | `internal/mysql/relational_spill.go` is **temporary** execution spill for large query working sets, not durable table storage |

Product decisions deliberately left physical organization as an implementation choice (#4, #9) while requiring one built-in row-oriented engine and durable commits.

## 3. Snapshot / Apply / ReplaceIfRevision

### Semantics

| API | Behaviour |
| --- | --- |
| `Snapshot()` / `SnapshotWithRevision()` | Mutex + **deep clone** of the entire `Definition` (including all rows); returns process-local `revision` |
| `Apply(definition, action)` | Clone → run mutation → validate; does not publish or persist |
| `Replace(definition)` | Validate + persist + install + bump revision |
| `ReplaceIfRevision(expected, definition)` | Fail with `ErrRevisionConflict` if `revision != expected`; else same as Replace |
| `mutate` / `Create*` / `Insert` / `ReplaceRows` | Internal helpers that clone + persist under the store lock |

Optimistic concurrency is **whole-catalog**: one writer wins; concurrent commit of a stale snapshot becomes SQL deadlock/retry (`1213`) or “catalog changed concurrently”.[source](../../internal/mysql/transaction.go)

### Callers that depend on this seam

| Caller | Use |
| --- | --- |
| `internal/mysql/transaction.go` | Transaction snapshots, autocommit, COMMIT publication |
| `internal/mysql/server.go` + DDL/DML helpers | `mutateCatalog` actions mutating `*Definition` |
| `internal/mysql/backup.go` | `catalog.Encode(Snapshot())` → backup `catalog.json` |
| `internal/catalog/accounts.go` | Account CRUD via `mutate` / `Snapshot` |
| `internal/lifecycle/server.go` | `Open` / validate / recover |
| `cmd/database/backup_restore.go`, `data_validation.go` | Expect `catalog.json` present and `catalog.Open`able |
| Unit tests under `internal/mysql/*_test.go` | `catalog.Open(tempDir)` fixtures |

Anything that keeps working through **the same Store methods** can hide a new physical layout. Anything that assumes `Table.Rows` is a cheap complete materialization, or that `catalog.json` alone is the full backup, must be updated.

## 4. Why the current design cannot meet #71

Normative gates (`docs/performance-acceptance.md`, issue #16/#71):

- Corpus: ~1M narrow + 1M related rows, **10 GB logical** column bytes
- 50 sessions
- PK/unique lookup ≥ **5000/s**, p95 ≤ 10 ms
- Durable insert / indexed update ≥ **500/s**, p95 ≤ 25 ms
- Clean start to ready ≤ **3 s** (9/10)

Fatal mismatches with whole-catalog JSON + deep-clone + full-table “index” scans:

1. **Writes are O(database size) per commit.** Each durable insert rewrites ~10 GB of indented JSON and fsyncs. 500 commits/s is impossible.
2. **Corpus load is quadratic.** Autocommit insert *N* times, each rewriting growing catalog JSON.
3. **Reads clone the world.** `currentDefinition()` / statement snapshots call `Snapshot()`, copying every row for every statement—even a PK lookup.
4. **Lookups scan/sort all rows.** Even when the planner selects a PK/unique index, `indexScanRows` builds keys for every row and sorts.
5. **Clean start cannot parse 10 GB JSON in 3 s**, and keeping all values as Go `string` cells will not fit comfortably in the 16 GB reference machine once headers/indexes/overhead are counted.
6. **Backup/validate** currently treat one `catalog.json` as the complete relational image—so a new layout must extend those operator paths.

## 5. ADRs and planning docs

| Doc / decision | Storage relevance |
| --- | --- |
| `docs/adr/` | **Missing** (domain docs say ADRs are created lazily) |
| Issue #4 resolution | One built-in row-oriented engine; no public engine choice; physical pages/buffers/WAL are implementation details |
| Issue #9 resolution | One durability level; acknowledged commit survives crash; recovery before ready; fail closed on corruption; no numeric recovery-time target |
| Issue #16 / `docs/performance-acceptance.md` | Quantitative gates above; storage layout not part of the product contract |
| `docs/catalog-metadata.md` | Storage-engine choice is not an application-facing feature |
| `docs/research/` | Only driver-compatibility research prior to this note |

## 6. Recommended smallest viable implementation (for #71)

**Deepen `catalog.Store`**—keep it as the single durable seam the MySQL layer already uses—rather than introducing a parallel engine or rewriting SQL execution wholesale.

### Target module shape

```
internal/catalog          # deep facade: Open, Recover, Snapshot*, Apply, Replace*
                          # schema+accounts stay small; row bodies leave Table.Rows-as-JSON
internal/storage/...      # private adapters: wal, heap/pages, btree/hash indexes, buffer
```

Callers continue to pass `func(*catalog.Definition) error` for DDL/schema and small tables, but large-table DML and lookups must stop depending on materializing `Table.Rows` for the full corpus.

### Minimal capability set (enough for the four gates + clean start)

1. **Separate schema from rows**
   - Keep namespaces/tables/columns/constraints/indexes/accounts in a small durable meta file (can remain JSON initially).
   - Store row payloads in append-friendly segments or fixed pages under the data directory (private layout).

2. **Durable commit without whole-catalog rewrite**
   - Append-only **WAL** (or equivalent redo) with one durability level: ACK only after the commit record is stable (issue #9).
   - **Group commit** is allowed and likely required for ≥500 durable txn/s: many sessions wait on one shared fsync of a WAL batch; do not expose relaxed durability.
   - Checkpoint/compaction in the background; recovery replays WAL before ready (`catalog.Recover` / lifecycle validate expands accordingly).

3. **Point lookup indexes that are real**
   - Maintain on-disk (or memory-mapped) **primary and unique** access paths for the acceptance tables.
   - Secondary index maintenance required for the indexed-update gate.
   - Change `indexScanRows` / PK equality path to **probe** the store for single-key lookups instead of sorting all rows. Full scans may remain for unindexed access.

4. **Stop deep-cloning row bodies on Snapshot**
   - Snapshot should be a cheap revision + immutable schema handle + MVCC/read view over row storage.
   - `cloneDefinition` copying every `[][]string` cannot survive 2M×10 GB.
   - Transaction isolation (#46 behaviour) stays at the Store interface: `SnapshotWithRevision` / `ReplaceIfRevision` can remain the publication gate, but the staged “definition” must not embed full row vectors for large tables—or Apply must operate as a logical redo list against the row store.

5. **Memory / clean-start discipline for the 16 GB reference host**
   - Do not keep the full logical 10 GB decoded as Go strings.
   - Open files / mmap / buffer pool sized so clean start is metadata + index roots open, not a full decode (≤3 s gate).
   - Warm-up (5 minutes) is what brings pages into cache for the 5000/s lookup gate.

6. **Operator surfaces**
   - Backup/restore/validate must include WAL + row/index files (or a stable export), not only `catalog.json`.
   - Prefer evolving `catalog.Encode` / backup packaging behind `internal/mysql/backup.go` and `cmd/database/backup_restore.go` rather than leaking storage files into the SQL layer.

### What not to do

- Do not revive `internal/engine` as the production store; it is unwired and still row-slice based.
- Do not put a full InnoDB clone in scope for #71; hash/B-tree for PK+unique+one secondary, WAL, and paged row heap is enough for the homogeneous gates.
- Do not weaken messgo (`config/messgo.xml` imports full `codesize` + `design` with no exclusions). Split storage into small packages/files; keep `catalog` a thin deep module. If size pressure appears, refactor production code—never the gate (issue #89 mandate).

### Suggested implementation order (tightest feedback loop)

1. **COW / no-row-clone Snapshot + in-memory PK/unique maps** while still on JSON — proves lookup path and removes clone tax on small fixtures; still fails durability/load/clean-start at 10 GB.
2. **WAL + segmented row files behind `ReplaceIfRevision`** — unlocks durable insert/update throughput and corpus load; keep schema JSON.
3. **Persistent PK/unique/secondary structures + probe in `indexScanRows`/DML** — unlocks 5000/s and indexed update.
4. **Startup that does not full-scan row payloads** — unlocks clean-start ≤3 s.
5. **Backup/validate/recovery diagnostics** aligned with issue #9/#65 contracts.

### Seam checklist (preserve)

- `catalog.Open` / `Recover`
- `Snapshot` / `SnapshotWithRevision` / `Apply` / `ReplaceIfRevision` / `ErrRevisionConflict`
- MySQL transaction publication in `internal/mysql/transaction.go`
- Lifecycle readiness only after recovery
- Single durability level (no ACK before stable commit)

### Expected breakages to budget for

- Tests and backups that round-trip `Table.Rows` through `catalog.json`
- Any code that treats `len(table.Rows)` or ranging `table.Rows` as cheap catalog metadata (explanations, `SHOW`, constraint checks on huge tables need store-backed counters/iterators)
- `Account()` calling full `Snapshot()` (should read schema/accounts only)

## 7. Messgo constraint

`make quality` runs default messgo `codesize` + `design` on all non-test Go (`CODING_STANDARDS.md`, `config/messgo.xml`, issue #89). New storage must be factored so packages stay within those defaults. Prefer many deep, small packages under `internal/storage` with `catalog` as the only MySQL-facing durable module.
