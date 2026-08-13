# Developer-friendly README positioning

Research date: 2026-08-13

Product baseline: [`origin/main` at `c6b4098`](https://github.com/jonbaldie/database/tree/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5)

## Decision summary

The README must lead with the application developer's problem, not with the
implementation language or the operator command list. The clearest documented
developer need is to inspect and test query behaviour. The strongest verified
solution is the stable query explanation, which gives plan
choices and runtime evidence in machine-readable and human-readable forms. The
MySQL application path makes the first trial familiar, but it is an access
method and not the main product position.[Domain language](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/CONTEXT.md#L3-L17)
[Query explanation contract](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/query-explanation/README.md#L3-L15)

The current README already contains these facts, but it gives the
implementation category before the developer outcome. Its quick start also
proves server startup, but it does not let the developer run a query or see a
query explanation. Thus, it stops before it proves the product's main
benefit.[Current opening](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/README.md#L1-L14)
[Current quick start](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/README.md#L16-L51)

## Best one-sentence position

> **database** is an experimental relational database for application
> developers who want to inspect and test query behaviour with tested MySQL
> clients and SQL.

This position identifies the application developer, the job, the product
category, and the access path. Each part has direct product evidence. Query
behaviour is the relationship between
a query, its selected execution, its results, and its performance
characteristics. The tested compatibility profile includes real MySQL clients,
text SQL, and prepared SQL.[Domain language](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/CONTEXT.md#L7-L17)
[Driver evidence](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/compatibility-evidence.md#L1-L21)

Do not put “written in Go” in this first sentence. It is useful project
information, but it does not tell an application developer why to try the
database.

## Pain points and solutions

The pain points below are positioning hypotheses. The repository proves the
solutions, but it does not prove how frequently developers have each pain.
Application-developer interviews or README response data must validate demand.

| Proposed developer pain point | Verified product solution | README proof to show |
| --- | --- | --- |
| “I can see that a query is slow, but I cannot see the selected work and its runtime behaviour in a form that my tests can use.” | Query explanation has a versioned JSON physical-operator tree and a stable table projection. `EXPLAIN ANALYZE` supplies complete runtime evidence. A live explanation supplies a non-blocking partial snapshot.[Query explanation forms](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/query-explanation/README.md#L3-L15) [Evidence semantics](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/query-explanation/README.md#L63-L74) | A small query, its `EXPLAIN ANALYZE FORMAT=JSON` command, and a short selected part of the result. |
| “I do not want a new client API before I can test a new database.” | The server uses the MySQL classic protocol. The verified profiles use Go, PHP, Node.js, Python, Java, and the MySQL CLI. The evidence names the exact tested versions and limits.[Driver evidence](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/compatibility-evidence.md#L9-L21) [Observed scenarios](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/compatibility-evidence.md#L47-L59) | One copyable connection example. Then link to the exact tested client matrix. |
| “An experimental database can leave me uncertain about what works and what fails.” | v0.1 has a finite MySQL 8.4.11-shaped SQL contract. Unsupported behaviour returns explicit protocol or operator errors. The public evidence map connects documented surfaces to black-box tests.[SQL boundary](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/mysql-sql-behaviour.md#L1-L19) [Conformance evidence](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/conformance.md#L9-L18) | A short “What works” list, a visible experimental warning, and direct links to the finite contracts and evidence. |
| “I want a local trial, not a cluster design task.” | database is a single-node server. The release supplies native binaries for three targets and a two-platform OCI image. One executable owns initialization, service lifecycle, backup, restore, validation, and version reporting.[Release artifacts](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/distribution.md#L29-L55) [Operator command family](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/operator-automation.md#L11-L16) | A quick start for one local instance. Put other installation methods after the first successful query. |
| “A failed statement can leave data or transaction state in an unclear condition.” | The SQL contract defines statement atomicity, transaction failure boundaries, savepoints, MySQL error numbers, and SQLSTATE values. The conformance map names public black-box evidence for these behaviours.[Transaction failures](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/mysql-sql-behaviour.md#L21-L42) [Application developer evidence](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/conformance-evidence.md#L50-L67) | One sentence about explicit and testable outcomes. Keep the complete rules in the SQL contract. |

The first pain point must have the most space. It is the only one that directly
matches the project's stated reason to exist. The other rows reduce trial risk
and support trust.

## Proposed README information order

1. **Name and position.** Use the one-sentence position above.
2. **Why try it.** Use no more than three short problem-and-solution points:
   inspect query behaviour, use a familiar MySQL application path, and rely on
   finite public contracts.
3. **Experimental boundary.** State that v0.1 is experimental, is not a drop-in
   MySQL replacement, and does not claim production readiness. Keep this before
   the quick start.[Current release boundary](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/CHANGELOG.md#L32-L35)
4. **Try it.** Give one complete path: install one binary, initialize one local
   instance, start it, connect with one tested driver, create a small table, run
   a query, and inspect it with `EXPLAIN ANALYZE FORMAT=JSON`.
5. **Decide if it fits.** Add short “Try it if” and “Do not use it if” lists.
   Suitable trial needs include local evaluation, application query tests, and
   the documented MySQL-shaped surface. Unsuitable needs include production
   service, complete MySQL compatibility, a distributed database, or an
   unsupported platform.
6. **Compatibility and SQL at a glance.** Name only tested client profiles and
   high-level SQL groups. Link to the exact matrices instead of copying the
   complete contract.
7. **Operate the local server.** Summarize shutdown, backup, restore,
   diagnostics, and structured command results. Keep the full command table
   below the application journey.
8. **Other installation methods.** Give native and OCI choices. Keep build from
   source as a contributor path.
9. **Help and contribution.** Give one clear route for questions and one route
   for defects or feature requests. Then name the contribution and governance
   documents.
10. **Evidence and policy.** Link to conformance evidence, compatibility policy,
    license, and the complete docs.

GitHub states that a README usually tells visitors what a project does, why it
is useful, how to start, where to get help, and who maintains and contributes
to it. GitHub also recommends that the README contain only the information that
developers need to start to use and contribute to the project.[GitHub README guidance](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-readmes#about-readmes)
The current README covers purpose, start, policy, and contribution links, but it
does not give a clear help route.[Current README](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/README.md)
The revised README must say where a developer can ask a usage question and
where a developer can report a defect. Do not invent a chat or support channel.
Use only a channel that the project owner selects and maintains.

This order follows a useful pattern in primary project sources. Dolt starts
with one distinct idea, then explains the familiar MySQL path before it gives
installation detail.[Dolt README](https://github.com/dolthub/dolt/blob/b81fedf22d040f459c522067eec9710e25e97589/README.md#L3-L17)
DuckDB starts with its category and value, then gives a very small SQL example
that proves a useful action.[DuckDB README](https://github.com/duckdb/duckdb/blob/5e66dad275e117531786fd9dddf4b75f4763873a/README.md#L16-L37)
CockroachDB separates product purpose, first-start steps, and tested driver
guidance.[CockroachDB README](https://github.com/cockroachdb/cockroach/blob/8812064a015d2faf99d3fc7e15880f94042954b0/README.md#L22-L57)

## Claims to avoid

| Do not claim | Reason |
| --- | --- |
| “Production-ready” | v0.1 explicitly does not make this claim.[Release boundary](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/CHANGELOG.md#L32-L35) |
| “Drop-in MySQL replacement” or “MySQL-compatible” without a limit | The supported surface is finite, and optional protocol features such as compression and multi-statement execution are not advertised.[SQL boundary](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/mysql-sql-behaviour.md#L1-L19) [Profile limits](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/compatibility-evidence.md#L47-L59) |
| “Works with any MySQL client” | The project has a named and versioned tested client set. State that set and link to its evidence.[Pinned clients](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/compatibility-evidence.md#L9-L21) |
| “High performance,” “fast,” or a production capacity promise | The performance targets are an experimental release gate, not a service-level agreement or a universal deployment promise. The audit also does not require the complete reference-machine run.[Performance boundary](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/performance-acceptance.md#L24-L39) [Release audit](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/conformance-evidence.md#L128-L141) |
| “Zero configuration” or “starts in one command” | The safe quick start needs explicit initialization, a password input, a data directory, and then `serve`.[Current quick start](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/README.md#L16-L51) |
| “Stable query plans” | The explanation document shape has a versioned stability contract. This does not promise that plan selection never changes. Estimates are comparable only in the same server version and planning context.[Explanation evolution](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/query-explanation/README.md#L63-L69) [Format evolution](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/query-explanation/README.md#L88-L98) |
| “Safer,” “simpler,” or “easier” without a specific comparison | These are comparative claims. Replace each one with the exact observable behaviour, such as password-file input, one executable, explicit errors, or versioned JSON. |

SQLite shows that a short benefit list can make a product easy to understand,
but its list also rests on mature evidence such as deployment count, tests, and
long-term format support.[SQLite overview](https://www.sqlite.org/about.html)
This experimental project must use its own narrower evidence and must not copy
that level of trust language.

## Source-backed content brief

### Audience and job

Write for an application developer who writes queries in an application and
tests their behaviour before release. Use “application developer,” “query
behaviour,” “query explanation,” “database namespace,” and “database account”
with the meanings in `CONTEXT.md`.[Domain language](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/CONTEXT.md#L7-L17)

### Message

Lead with this outcome: the developer can inspect and test query behaviour.
Explain that a query explanation shows selected operator work, estimates,
runtime evidence, resource use, and live partial evidence in stable JSON and
table forms. Do not reduce this to a query log or debug text.[Operator and JSON contract](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/query-explanation/README.md#L17-L45)

### Proof journey

The first journey must end with the product benefit, not only with a running
process. It must do these actions:

1. Install or select the v0.1.0 binary for one supported platform.
2. Initialize a data directory with a password file or standard input.
3. Start `database serve` on loopback.
4. Connect with the Go MySQL driver, because its profile is always in the
   repository test set.[Driver reproduction](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/compatibility-evidence.md#L23-L29)
5. Create one database namespace and table, insert a few rows, and run a query.
6. Run `EXPLAIN ANALYZE FORMAT=JSON` for the query and point to two or three
   useful fields. Use the checked example shape as the source for sample output.
   [Checked analysis example](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/query-explanation/examples/analyze.json)

### Trust and limits

Keep the experimental warning visible. Follow it with exact evidence: the
finite SQL contract, named tested clients, black-box conformance map, supported
release targets, and Apache License 2.0. This gives the developer facts to make
a trial decision without a broad readiness claim.[Conformance seams](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/conformance-evidence.md#L15-L48)
[Distribution contract](https://github.com/jonbaldie/database/blob/c6b4098c0de7e653e06c057f1dc39f5ce0bbcec5/docs/distribution.md#L7-L27)

### Tone and length

Use direct second-person instructions in the trial. Use short sections and
small code samples. Define a product term before a later section depends on it.
Move detailed operator tables, quality-gate detail, and the complete document
index below the first application journey. Keep exact restrictions near the
step that they affect. Add a short help section with the selected question and
defect routes.

## Research boundary

This note uses product contracts, source, tests, release evidence, and official
project sources. It does not use review sites, popularity lists, benchmark
comparisons, or unsourced statements about developer preferences. The proposed
pain points are positioning hypotheses until direct developer research validates
them.
