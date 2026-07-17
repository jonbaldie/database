# v0.1 performance acceptance scenario

This document is the normative v0.1 **performance acceptance scenario**. It
defines the fixed corpus, application-visible operations, load boundary,
warm-up, measurement, repetition, release judgment, clean-start gate, and
published evidence required before v0.1 release. It is release evidence, not
an automated CI test, service-level agreement, or universal promise about every
deployment.

The scenario deliberately uses a neutral relational corpus. It does not invent
a representative application, prescribe an implementation benchmark, or make
storage layout, caching, scheduling, worker counts, memory allocation,
measurement-client architecture, or other internal mechanics part of the
product contract.

## Versioning and reference environment

The **v0.1 performance acceptance corpus** and this scenario share an explicit
version. A material change to either the data shape, distribution, operation
definition, load boundary, measurement rule, or release judgment creates a new
version. Results from different versions are not directly comparable. Every
release-evidence record names the scenario and corpus versions it used.

The **v0.1 capacity envelope** is approximately 10 GB of stored application
data and 50 simultaneous sessions on one ordinary machine. The 10 GB corpus
size below counts logical application column values after loading; indexes,
catalogs, temporary data, and internal overhead do not count toward it. The
capacity envelope is an acceptance boundary for the experimental release, not a
production-scale guarantee.

The **v0.1 performance reference environment** is an Apple iMac `Mac15,5` with
an eight-core M3 chip, 16 GB of memory, and its internal 512 GB Apple SSD. Each
result records the operating-system version and relevant test conditions. The
published machine is the reference for release judgment; results from another
machine are diagnostic evidence, not substitutes for the gate.

## Acceptance corpus

The fixed, versioned corpus is deliberately non-domain and contains 10 GB of
logical application column values after loading. Its two neutral relational
record families are:

1. **Narrow keyed records** with primary, unique, and secondary access paths.
2. **Larger related records** with their own key, parent reference, secondary
   key, timestamp, numeric value, and payload.

A recorded seed produces deterministic, uniformly distributed values. Gated
requests select uniformly across the complete keyspace, so a small hot set
cannot define acceptance. Skewed or hotspot results may be published as
diagnostics but are not release gates.

The corpus loader records exact row counts and logical size. Indexes, catalogs,
temporary data, and internal overhead are reported separately when relevant;
they do not change the corpus's 10 GB logical-size definition.

## Operation gates

Acceptance uses four separate homogeneous runs against the same corpus state:

| Gate | Operation | Thresholds that must hold in every passing run |
| --- | --- | --- |
| Primary-key lookup | Look up one narrow keyed record by its primary key | p95 ≤ 10 ms; p99 ≤ 40 ms; ≥ 5,000 successful reads/s |
| Unique-key lookup | Look up one narrow keyed record by its unique key | p95 ≤ 10 ms; p99 ≤ 40 ms; ≥ 5,000 successful reads/s |
| Durable insert | Insert one row and commit it as its own durable transaction | p95 ≤ 25 ms; p99 ≤ 100 ms; ≥ 500 successful committed transactions/s |
| Indexed update | Update one row, changing an ordinary value and a secondary-indexed value, and commit it as its own durable transaction | p95 ≤ 25 ms; p99 ≤ 100 ms; ≥ 500 successful committed transactions/s |

Each measured run begins from the same corpus state. Inserts use a fixed
unused-key range. Indexed updates follow a deterministic sequence. The corpus
is restored before another measured run, so prior measured mutations do not
change a later run's starting state.

The latency and throughput thresholds in a row apply simultaneously to that
run. There is no additional mixed-workload or low-concurrency release gate.

## Application-visible load boundary

The canonical measurement path is the current stable `go-sql-driver/mysql`
version recorded for the release. It uses server-prepared statements over
loopback without TLS. Other named drivers, text queries, and TLS remain
compatibility evidence rather than additional performance gates.

Every operation gate uses 50 authenticated sessions. Each session has at most
one operation in flight and immediately submits its next operation after the
previous one completes; all 50 sessions participate throughout measurement.
The workload therefore measures application-visible concurrency rather than an
internal worker count.

## Warm-up and measurement

Measurement starts only after all of these conditions hold:

- the corpus is loaded and its version and exact logical size are recorded;
- the server reports ready;
- all 50 sessions are authenticated; and
- an unmeasured five-minute warm-up has exercised the same operation across the
  full keyspace.

Each measured run lasts at least five minutes and completes at least 100,000
operations; both minimums are required. Only operations submitted after
measurement begins and completed before it ends enter the latency distribution
and throughput count. Operations still in flight at either boundary are
reported separately.

Every completed attempt is retained. Slow attempts are never discarded as
outliers. An unexpected database error fails the run. Explicit timeout,
resource-exhaustion, constraint, or contention outcomes do not count as
successful throughput and cannot satisfy a gate. A database-caused connection
loss or server termination fails the run.

A run may be excluded only for a documented external invalidation, such as
unrelated host activity, an environmental interruption, or failure of the
measurement client itself. Every excluded run and its reason is published.

## Repetition and release judgment

Each operation gate is run independently five times. A run is valid only when
it was not excluded under the rule above. At least four of the five valid runs
must satisfy every latency and throughput threshold for that gate. One failed
run is published but does not block release; two or more failed runs block
release until the cause is corrected.

Percentiles and throughput are judged per run. An aggregate distribution may be
published for context, but it cannot conceal a failing run. The final release
judgment names every valid pass and failure and every excluded run.

## Clean-start gate

The clean-start gate uses the fixed 10 GB corpus after a clean shutdown. No
recovery, upgrade, restore, initialization, or validation work may be pending.
Startup time begins when the operator invokes the `database` command and ends
when the first supported-driver session authenticates and successfully pings
the ready server.

Ten independent clean starts are measured. At least nine must complete within
three seconds. A server failure to become ready is a failed attempt. Initialization,
recovery, restore, and upgrade retain their correctness and diagnostic
contracts, but have no numeric duration target in this scenario.

## Published release evidence

Release evidence records all of the following:

- server version;
- scenario and corpus versions;
- reference operating-system and driver versions;
- effective public configuration;
- exact corpus row counts and logical size;
- operation definitions and load boundary;
- per-run throughput, p50, p95, and p99;
- operation errors and unfinished operations;
- every excluded run and its documented reason;
- every clean-start duration; and
- the final acceptance judgment.

This evidence makes the release decision reproducible without turning the
scenario into a representative application workload. Storage layout, caching,
worker counts, optimizer organization, disk amplification, memory allocation,
and measurement-client architecture remain implementation choices unless they
alter an observable contract stated here.
