# Indexed range page performance

This is diagnostic evidence for one task-list query. It is not the v0.1 release acceptance run.

## Result

Five alternating runs per version measured one prepared MySQL request at a time over loopback without TLS. Each request returned 20 task identifiers and titles from a 100,000-row table. The client checked every identifier, title, and row count before the timer stopped.

The median of the run means fell from 450.535 microseconds to 185.597 microseconds per request. Throughput increased from 2,219.6 to 5,388.0 requests per second. This is a 142.75% throughput increase and a 58.81% time reduction. These values are not individual-request latency percentiles.

The baseline range was 435.300 to 486.755 microseconds. The changed range was 179.019 to 195.980 microseconds. Each version completed 50,000 requests and verified 1,000,000 rows without errors. No run was excluded.

[comparison.json](comparison.json) contains all ten runs and their raw Go benchmark output.

## Query path

The client prepares this query once per server start.

```sql
SELECT id, title
FROM tasks FORCE INDEX (idx_created)
WHERE created >= ? AND created < ?;
```

The table contains integer `id` and `created` columns, a `VARCHAR(64)` title, a primary key on `id`, and an index on `created`. Each row has identical `id` and `created` values. Titles have the form `task-123`. Request starts follow `iteration * 7919 % 99981`, so requests do not repeat one page.

The server resolves one immutable catalog snapshot. `indexScanRows` uses the cached ordered index and its lower and upper bounds. `tableRowIterator` then copies the selected rows. `StreamWriter` encodes the rows in the prepared-statement binary protocol. The driver reads and checks the complete result.

Previously, `tableRowIterator` also called `sourceRowBytes` on all 100,000 rows before it returned the first selected row. Ordinary queries have no runtime metrics object. The resulting byte count had no consumer.

The change puts that byte count and its recording call inside the existing `runtime != nil` condition. An analyzed query still collects the same storage evidence. No index, SQL rule, cache policy, or public signature changed.

## Measurement conditions

The host was an Apple M3 with eight cores and macOS 26.6.2. Both versions used Go 1.26.9, `go-sql-driver/mysql` 1.9.3, default server limits, and two Go runtime processors. The baseline production source was commit `8039491`.

Both test binaries used the same benchmark source. The baseline had no production edits. The changed binary had only the runtime-metrics guard. Builds did not use race instrumentation. Corpus loading, server startup, authentication, preparation, and the first request were outside the timer. The first request built the ordered-index cache on both versions.

The host was shared. The initial load averages were 4.08, 5.07, and 5.03. Other active processes included iTerm2 and WindowServer. The benchmark ran before, after, before, after to reduce drift between versions. The two timing ranges do not overlap.

Each timed run had 10,000 complete requests. The benchmark used real TCP connections, authentication, server-prepared statements, the relational executor, and the MySQL driver. Catalog loading used a fixed fixture before the timer. This measures steady-state reads, not SQL import performance or process startup.

## Limiter evidence

[before-top.txt](before-top.txt) and [after-top.txt](after-top.txt) contain separate CPU profiles. Profiling started after setup and cache warm-up. Profile runs are not part of the reported timing runs.

The baseline profile observed `sourceRowBytes`. The changed profile did not observe that function. Both profiles locate most samples in socket writes and network polling. The changed profile still records 1.69 seconds of cumulative samples under `StreamWriter.writeRawPacket` for 10,000 requests. Wire transfer is the measured remaining limit. These sampled values are not an exact wall-time decomposition.

[before-writer-profile.txt](before-writer-profile.txt) and [after-writer-profile.txt](after-writer-profile.txt) retain the writer call paths. The sampled writer cost also fell. The inference is that removal of the delay before the first row changes client and server socket scheduling. No claim depends on an exact share for that effect.

A separate baseline diagnostic added a timer around the byte-count call. [byte-accounting-timing.txt](byte-accounting-timing.txt) records five runs. The call took a median 198.589 microseconds per request, with a range of 192.822 to 251.472 microseconds. This diagnostic is not a second speedup measurement. The timer edits did not ship.

[source-bytes.txt](source-bytes.txt) records five isolated byte-count runs against the same row values. Their median was 215.232 microseconds. The isolated result supports the full-table cost but does not replace the wire measurement.

The changed response contains the same 20 small rows. The improvement does not claim a disk or network bandwidth increase. Allocations remained at 1,567 per request. The client checks rule out errors, an empty result, or a skipped request as the explanation.

## Design choice

Two shapes were considered in sequence because this harness has no subagent tool.

The selected shape keeps the existing optional `selectRuntimeBinding` as the owner of diagnostic work. A local condition prevents the unused full-table traversal. It changes no function boundary.

The rejected shape adds a lazy row-byte argument or a new runtime-recording method that accepts table rows. It would centralize byte counting across scans and joins, but it expands the internal interface and write scope. The one-table change already meets the target. Join instrumentation remains unchanged.

Model the Domain kept the existing optional runtime object instead of adding another flag. Laziness Protocol kept the fix local and rejected the new interface. Build the Lever produced the checked-in comparison script. Explain the Number required alternating runs, result checks, and separate profiles. Sequence Work into Verifiable Units required a successful focused test run before the repeated comparison. Test Behavior, Not Implementation required exact results through TCP and literal storage-evidence checks. Prove It Works required a built-executable test and the project gates.

## Verification

`GOMAXPROCS=2 GOFLAGS='-p=2' make quality` passed. This includes formatting, vet, race tests, the build, messgo, and govulncheck. Govulncheck found no reachable vulnerabilities.

`GOMAXPROCS=2 GOFLAGS='-p=2' make mutation` passed. All three changed-line mutants were killed. Overall and covered-code mutation scores were 100%.

`TestIndexedRangePreservesRowsAndAnalyzeStorageEvidence` uses the built executable. It checks the exact range result before and after `EXPLAIN ANALYZE`. It also checks two scan rows, two logical reads, and the unchanged byte count of 12 for its three-row corpus.

The evidence audit covered that new process test and the two new benchmark cases. All three are production-grounded. No test-local, test-layer-only, or unclear result was found in this scope. The process test observes wire results. The page benchmark observes decoded driver results. The byte-count benchmark observes the production function's return value.

The repository's required `verify-database` skill was absent. The Claude skill path was a broken link to the missing Agents path. That skill did not run. The process test and real TCP benchmark supplied direct product evidence instead.
