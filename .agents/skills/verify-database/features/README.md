# database verification map

This directory is the maintained source for verifying the user-facing behavior
of the `database` server. Read this index before driving the product, then use
the matching feature file as the recipe.

## Baseline preconditions

- Run every command from the repository root.
- Start one isolated instance with
  `.agents/skills/verify-database/helpers/control.sh start` and keep the printed
  `RUN` value. All later subcommands take it.
- The run owns its own free ports, data directory, and admin password. Never
  drive an instance this run did not start, and never point a command at
  another run's data directory.
- Run `control.sh doctor <run>` and require all seven lines: `process`,
  `version`, `owned`, `ready`, `live`, `lock`, `auth`.
- The admin account is `admin`; its password is in the run's `PASSWORD_FILE`,
  which `control.sh sql` reads for you.
- There is no `mysql` CLI. All SQL goes through `control.sh sql`.

## Driving conventions

- Start every recipe from a fresh instance unless its preconditions say
  otherwise.
- One `control.sh sql` call is one session. `USE`, `BEGIN`, `SET`, and
  savepoints persist inside the call and are lost between calls.
- Treat every command as literal. Keep quoted SQL, identifiers, and flags
  unchanged.
- Assert on stable handles: MySQL `error_code` numbers, JSON field names from
  `database.operator.result/v1`, `database.lifecycle/v1`, and
  `database.explain/v1`, and the documented endpoint paths. Never assert on
  human wording.
- Two sessions at once need two concurrent `control.sh sql` calls; run one in
  the background.

## Proof and skip reporting

- Capture the mutating statement and the resulting state, not only the final
  read.
- Wire proof includes the statement JSON line with `ok`, `columns`, `rows`, and
  `error_code` where relevant.
- CLI proof includes the command, stdout, stderr, and exit code.
- Mutation proof includes a second independent view of the stored value: the
  on-disk `control.sh catalog <run>`, or a re-read after
  `control.sh restart <run>`.
- A privilege, constraint, or limit is proven only when the forbidden case
  fails with the documented `error_code`.
- Write artifacts to `/tmp/verify-database-evidence/<run>/` and name the run
  identifier and feature ID with each one.
- Report an unreachable path with the attempted command and the unmet
  precondition. Do not report a skipped entry point as verified through a
  different path.

## Feature entry contract

Each feature file starts with an H1 title and one paragraph describing the
user-visible behavior. It then uses exactly four H2 sections in this order.

1. `Sub-features` lists short IDs with one line for each behavior.
2. `How to get to it (user POV)` lists every user entry point.
3. `Driving it with control.sh` starts with `Preconditions:` and pairs each user
   action with an exact command and an observable result.
4. `Gotchas` lists traps that can waste or invalidate a verification run.

## Features

- [Operator lifecycle](./operator-lifecycle.md) covers `init`, `serve`,
  readiness events, exclusive directory ownership, and graceful stop.
- [Diagnostics endpoints](./diagnostics-endpoints.md) covers `/live`, `/ready`,
  and `/metrics` on the diagnostics listener.
- [SQL data and durability](./sql-data-and-durability.md) covers databases,
  tables, mutations, constraints, and survival across a restart.
- [Transactions and savepoints](./transactions-and-savepoints.md) covers
  `BEGIN`, `COMMIT`, `ROLLBACK`, savepoints, row locks, and rollback on stop.
- [Account administration](./account-administration.md) covers `CREATE USER`,
  `GRANT`, `REVOKE`, `ALTER USER`, and privilege boundaries.
- [Query explanation](./query-explanation.md) covers `EXPLAIN` in the versioned
  JSON and traditional tabular forms.
