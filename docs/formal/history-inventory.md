# Original history inventory

This inventory records the source review at fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Status statements below describe that review. See [coverage.md](coverage.md) for the final bounded models and checks.

You asked for the implicit state machines in `jonbaldie/database`. I found **36 distinct histories**, grouped below by the part of the system that owns them. Their states are encoded in fields, files, call order, or collections; their transitions are spread across functions.

The review also found a reproducible packet-framing bug: the reader rejects a valid message split across two frames.

### Process and durable data

1. **Instance initialization.** Absent → initialization claimed → catalog installed → metadata installed → claim released. A crash can leave intermediate files for the next attempt to handle. [instance.go](../../internal/instance/instance.go#L69)

2. **Server lifecycle and clean shutdown.** Starting → optionally recovering → ready → shutting down → stopped. A durable running marker distinguishes an unclean stop from a clean one, while a file lock controls exclusive access. [server.go](../../internal/lifecycle/server.go#L160)

3. **Version upgrade.** Stopped instance → upgrade marker installed → metadata marked incomplete → target metadata installed → marker removed. A later attempt can resume an incomplete upgrade. [upgrade.go](../../cmd/database/upgrade.go#L212)

4. **Backup and restore.** Backup captures files in chunks, writes an archive to a temporary path, then installs it. Restore validates and stages the archive before installing its entries. Installation into an existing destination moves entries one at a time. [online_backup.go](../../cmd/database/online_backup.go#L136), [backup_restore.go](../../cmd/database/backup_restore.go#L947)

5. **Shutdown request and client observation.** The server latches a shutdown request once. The client polls until it observes enough consecutive connection failures or reaches its timeout. [shutdown.go](../../internal/mysql/shutdown.go#L42), [shutdown.go](../../cmd/database/shutdown.go#L120)

6. **Storage engine and transaction.** The engine moves from open to closed. A storage transaction builds an overlay, writes and syncs the WAL, publishes its changes, then marks itself finished. A failed commit does not mark it finished. [engine.go](../../internal/storage/engine.go#L64), [txn.go](../../internal/storage/txn.go#L290)

7. **WAL recovery.** Recovery tracks whether a transaction is pending. It collects records after a begin, applies them on commit, and leaves an unfinished transaction unapplied at end of file. [wal.go](../../internal/storage/wal.go#L88)

8. **Checkpoint and WAL rotation.** A checkpoint moves through snapshot creation, file publication, and WAL replacement. Recovery selects a checkpoint generation and replay position from the files left behind. [checkpoint.go](../../internal/storage/checkpoint.go#L63)

9. **Storage table schema.** A table is absent, created, reshaped, or dropped. Schema changes rebuild indexes and advance a version before metadata persistence completes, so an error can leave memory and disk at different stages. [meta.go](../../internal/storage/meta.go#L11)

10. **Catalog startup and legacy row migration.** Startup opens each table, prefers nonempty stored rows, or seeds storage from rows embedded in the catalog. This is a separate choice from WAL replay. [rows.go](../../internal/catalog/rows.go#L118)

11. **Catalog publication.** Requests enter a queue, form a batch, pass revision checks, apply changes, sync rows, persist schema, publish a revision, and notify waiters. [writer.go](../../internal/catalog/writer.go#L79), [publish.go](../../internal/catalog/publish.go#L222)

12. **Index caches.** An index is absent, built from rows, and replaced when its underlying row image changes. [ordered_index.go](../../internal/catalog/ordered_index.go#L12)

13. **Auto-increment allocation.** Allocation moves among disabled, uninitialized, next-value, and exhausted conditions. Generated or explicit values can advance it; truncation resets it. [auto_increment.go](../../internal/mysql/auto_increment.go#L10)

### Connections and sessions

14. **Connection admission and draining.** A connection is pending authentication, admitted, and eventually closed. During shutdown the server rejects new work and waits for admitted statements to finish. [server.go](../../internal/mysql/server.go#L160)

15. **Authentication handshake.** Greeting → optional TLS → authentication response → optional full-password or RSA exchange → success or failure. The permitted next packet depends on the path taken. [authentication.go](../../internal/mysql/authentication.go#L18)

16. **Connection command and cancellation.** A connection moves between idle and executing a command, with cancellation, revocation, and close paths. Read-ahead preserves a subsequent command while the current one runs. Idle transaction timeout can roll back the transaction. [connection.go](../../internal/mysql/connection.go#L128), [idle_timeout.go](../../internal/mysql/idle_timeout.go#L31)

17. **SQL transaction and savepoints.** A transaction begins, creates its snapshot lazily, accumulates reads and writes, then commits or rolls back. One-shot transaction settings are consumed at begin. Savepoints retain named snapshots of transaction changes; rolling back to one does not restore locks. [transaction.go](../../internal/mysql/transaction.go#L362), [transaction.go](../../internal/mysql/transaction.go#L680)

18. **Row locks and waits.** Locks can be shared or exclusive, wait, upgrade, time out, be cancelled, participate in deadlock detection, and be released. A failed locking select restores its prior lock snapshot. [locking.go](../../internal/mysql/locking.go#L38), [relational_select.go](../../internal/mysql/relational_select.go#L361)

19. **Prepared statements.** A statement is allocated, can cache parameter types and accumulate long data, then can execute, reset, or close. Resetting the connection closes its prepared statements. [server.go](../../internal/mysql/server.go#L4731)

20. **Session settings and selected database.** Settings persist across statements; some transaction settings apply only to the next transaction. Multi-assignment `SET` applies assignments in order and stops on error, leaving earlier assignments applied. [session_settings.go](../../internal/mysql/session_settings.go#L228)

21. **Accounts and revocation.** Accounts are created, changed or locked, and dropped. Locking or dropping an account also affects existing sessions and subsequent authentication. [account_administration.go](../../internal/mysql/account_administration.go#L291)

22. **Statement resources.** Memory and temporary-space reservations accumulate and are released. The statement also has failure, finalization, and close paths; finalization changes how later cancellation or deadlines affect durable work. [resources.go](../../internal/mysql/resources.go#L187)

23. **Warnings.** Ordinary statements clear the warning area, execution adds warnings or a failure, and `SHOW WARNINGS` reads the retained area. Connection reset clears it. [statement_policy.go](../../internal/mysql/statement_policy.go#L35)

24. **Live query explanation.** A running query registers its explanation, exposes snapshots while active, and removes its entry when finished. The removal checks identity so an older query cannot remove a replacement entry. [live_explanation.go](../../internal/mysql/live_explanation.go#L13)

25. **Packet and result streaming.** Packet framing tracks sequence numbers across frames. Result streaming progresses through metadata, rows, and an end marker, with errors possible after output has started. [stream_writer.go](../../internal/mysql/stream_writer.go#L25)

### Query execution and parsing

26. **Result ownership.** A result holds a release callback until it is consumed or closed; the callback is cleared and called once. [server.go](../../internal/mysql/server.go#L702)

27. **External sorting.** Rows accumulate in memory, spill into temporary runs when needed, merge, produce output, and clean up temporary files. [relational_spill.go](../../internal/mysql/relational_spill.go#L18)

28. **Offset, limit, and locking selection.** The executor skips offset rows, emits up to the limit, and accounts for rows unavailable under `SKIP LOCKED` as it selects candidates. [relational_select.go](../../internal/mysql/relational_select.go#L473)

29. **CTEs and subqueries.** A CTE moves from unevaluated to materialized and can then be reused. Uncorrelated subqueries can cache results; correlated ones run per outer row. A scalar subquery also tracks zero, one, or too many rows. [composed_select.go](../../internal/mysql/composed_select.go#L43), [composed_select.go](../../internal/mysql/composed_select.go#L581)

30. **Window functions.** Evaluation tracks partitions and peer groups, rank, and moving frame boundaries. Rolling aggregates add and remove values as the frame moves. [relational_select_window.go](../../internal/mysql/relational_select_window.go#L12)

31. **Joins.** Join execution builds or scans one side, probes candidates, records matches, and emits unmatched rows when required by the join type. [relational_select_source.go](../../internal/mysql/relational_select_source.go#L1097)

32. **Set operations and membership.** Set operators track seen rows or multiplicities and fold results in the required order. Membership evaluation tracks matches and unknown results caused by nulls. [composed_select.go](../../internal/mysql/composed_select.go#L1441)

33. **Ordered mutations and schema alteration.** Ordered updates and replacement process candidate rows and their effects in sequence. `ALTER` builds and validates a changed schema before publication. [server.go](../../internal/mysql/server.go#L2176), [ddl.go](../../internal/mysql/ddl.go#L866)

34. **Index-range construction.** Planning starts with absent bounds and tightens lower and upper bounds, retaining whether each is inclusive or exclusive. [relational_select_source.go](../../internal/mysql/relational_select_source.go#L764)

35. **SQL and command parsing.** Scanners retain quote, escape, comment, and nesting state. Parsers retain position, nesting, option presence, and constraints such as mutually exclusive options or duplicate modifiers. These are algorithmic state machines, rather than durable lifecycles. [lexical.go](../../internal/mysql/lexical.go#L201), [expression_parse.go](../../internal/mysql/expression_parse.go#L15), [config.go](../../cmd/database/config.go#L148)

36. **Archive validation.** Restore tracks the manifest, paths already seen, and the nesting of streamed JSON as it validates the archive. [backup_restore.go](../../cmd/database/backup_restore.go#L641)

The repository’s support tools also have workflows: the performance harness cycles through preparation, warm-up, measurement, restore, and evidence output; the black-box runner starts a process and waits for recovery and readiness; and the release builder produces binaries, an image archive, checksums, and a manifest. [performance/main.go](../../scripts/performance/main.go#L234), [runner.go](../../test/blackbox/runner.go#L228), [build-release.sh](../../scripts/build-release.sh#L50)

### Reproduced finding

The packet reader increments its expected sequence twice after the first frame. The writer emits the next consecutive sequence number, so the reader rejects a valid two-frame payload with `packet continuation sequence mismatch`. [stream_writer.go](../../internal/mysql/stream_writer.go#L346)

I used the **Prove It Works** principle to run the real writer and reader together in a temporary Go test overlay. The test failed with that error. No fix or repository change was made.

The inventory covers every production Go file, the runnable support scripts, and the black-box runner at commit `fc78c541`. It excludes individual test cases, CI workflows, agent templates, the Makefile, and Dockerfiles. I included histories that affect behavior or protocol, but excluded ordinary loop counters and measurement accumulators. The working tree remains clean. The review took about 6 minutes.