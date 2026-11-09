# Storage detail results

Source baseline is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. The combined verify-storage-recovery/run completed with exit 0. Product source remains unchanged.

| Model | Current states | Broken controls |
| --- | --- | --- |
| CheckpointPublication | 15 | Missing snapshot loses the committed row. Missing fence leaves the writer open. |
| SchemaVersion | 5 | Missing version guard publishes a stale transaction. |
| CatalogHydration | 4 | Legacy rows replace the durable image. |
| CatalogRevision | 3 | Two requests claim one revision. |
| OrderedCache | 4 | A changed row image reuses the stale index. |
| AutoIncrement | 17 | Missing advance or exhaustion permits reuse. |

All six current models passed. All eight broken models failed their named rules. Real storage, catalog, and MySQL probes passed. Eight temporary source overlays failed their named behavioral assertions. The runner rejects build failures as negative evidence. The combined command also passed the commit/recovery checks and delegated WAL checks.

Checkpoint probes stop at four hooks and check rows after close and reopen. A separate sync failure checks writer fencing. The schema probe uses a same-width rename so that ordinary width validation cannot mask the missing version guard. Catalog probes use real helpers sequentially. Allocation probes cover all 255 unsigned tinyint values and advance/exhaust behavior within limits 1 through 255.

These are finite checks. The model identifier limit is three. Checkpoint hook returns are not process kills. Disk power-loss ordering, arbitrary metadata failures, concurrent cache and writer schedules, and arbitrary numeric types remain outside scope. Exact source boundaries and maintenance rules are in the skill.

Current chart/report pairs are in checkpointpublication, schemaversion, cataloghydration, catalogrevision, orderedcache, and autoincrement. The reports were generated from checked graphs. All current and broken chart commands returned their expected status. Same-label steps are omitted from the drawings; no cross-label edges are explicitly hidden.
