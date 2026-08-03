---
name: verify-database
description: Launch and drive the `database` server (Go MySQL-wire relational database) to prove user-visible behavior end to end - operator commands, the MySQL wire protocol, SQL, durability, accounts, and diagnostics endpoints. Use when asked to run the server, confirm a change works in the real product rather than in unit tests, reproduce an operator or SQL bug, or capture verification evidence.
---

# Verify the database server

`database` is a single Go binary with two user-facing surfaces:

1. **The operator CLI.** `database init`, `serve`, `version`, `config`, and the
   `backup`, `restore`, `upgrade`, `data`, `shutdown` family. Contract:
   [`docs/operator-automation.md`](../../../docs/operator-automation.md).
2. **The MySQL wire server plus its diagnostics HTTP listener.** Clients speak
   the MySQL classic protocol with `caching_sha2_password`. Contract:
   [`docs/mysql-sql-behaviour.md`](../../../docs/mysql-sql-behaviour.md).

There is no `mysql` CLI on this machine. Drive SQL through the shipped
`sqlclient` helper, which uses `github.com/go-sql-driver/mysql`.

All verification runs through one script:

```
.agents/skills/verify-database/helpers/control.sh
```

Run it from the repository root. Every subcommand after `start` takes the run
identifier that `start` printed, because shell state does not persist between
tool calls.

## Launch

```sh
.agents/skills/verify-database/helpers/control.sh start
```

`start` builds `bin/database` if it is missing (`make build`), makes an
isolated run directory `/tmp/verify-database-<run>/`, writes a disposable
12-plus byte password file, runs `database init`, then starts `database serve`
with `--format=json` on two free loopback ports. It waits for the
`{"schema":"database.lifecycle/v1","state":"ready",...}` line on stdout before
it returns, then prints the run facts:

```
RUN=1785753623-22870
DATA_DIR=/tmp/verify-database-<run>/data
PASSWORD_FILE=/tmp/verify-database-<run>/password
MYSQL_ADDRESS=127.0.0.1:60974
DIAGNOSTICS_ADDRESS=127.0.0.1:60975
EVIDENCE_DIR=/tmp/verify-database-evidence/<run>
```

Keep the `RUN` value. Give it to every later subcommand.

**Isolation is complete.** Ports are free ports, the data directory is unique
per run, and the admin password is unique per run. Several runs can go at the
same time. One data directory is owned by exactly one live `serve` process; a
second `serve` on the same directory fails with `already in use`. Never drive
an instance that this run did not start.

Teardown: `control.sh stop <run>` (graceful `SIGTERM`, data directory kept) or
`control.sh clean <run>` (stop plus delete the run directory).

## Doctor

Run this first whenever anything looks wrong. It is read-only.

```sh
.agents/skills/verify-database/helpers/control.sh doctor <run>
```

It reports, and fails loudly on, each of:

- `process:` the PID this run started is alive.
- `version:` `database.version/v1` identity - product version, build identity,
  platform, compatibility profiles.
- `owned:` the count of `ready` events in this run's own `serve.log`.
- `ready:` `{"status":"ready"}` from `GET /ready` on this run's port.
- `live:` HTTP `200` from `GET /live`.
- `lock:` `.running.lock` present in the data directory, so the process owns it.
- `auth:` `SELECT 1` over the MySQL wire as `admin`, so credentials work.

A healthy instance prints all seven. Anything else means the instance is not
worth driving: read `control.sh log <run>` next.

## Drive

**SQL over the MySQL wire.** All statements in one call share one session, so
`USE`, `BEGIN`, `SET`, and savepoints carry over. Statements in separate calls
do not.

```sh
.agents/skills/verify-database/helpers/control.sh sql <run> \
  'CREATE DATABASE shop' \
  'USE shop' \
  'CREATE TABLE orders (id INT PRIMARY KEY, total INT)' \
  'INSERT INTO orders VALUES (1, 250)' \
  'SELECT id, total FROM orders'
```

Each statement prints one JSON line, and the exit code is `1` if any statement
failed:

```json
{"statement":"SELECT id, total FROM orders","ok":true,"columns":["id","total"],"rows":[["1","250"]],"rows_affected":0}
{"statement":"SELECT * FROM missing","ok":false,"rows_affected":0,"error":"Error 1146 (42S02): table does not exist","error_code":1146}
```

A failure is data, not a crash: use `error_code` to assert the MySQL error
number a contract requires.

**As a non-admin account** (needed for grant and privilege checks):

```sh
.agents/skills/verify-database/helpers/control.sh sql <run> \
  --user reader --password reader-password-1 'USE shop' 'SELECT total FROM orders'
```

**Diagnostics HTTP:**

```sh
.agents/skills/verify-database/helpers/control.sh diag <run> ready     # {"status":"ready"}
.agents/skills/verify-database/helpers/control.sh diag <run> live
.agents/skills/verify-database/helpers/control.sh diag <run> metrics   # Prometheus text
```

**Durability across a restart** (graceful stop, then serve the same directory):

```sh
.agents/skills/verify-database/helpers/control.sh restart <run>
```

**Stored state and process output:**

```sh
.agents/skills/verify-database/helpers/control.sh catalog <run>   # on-disk catalog.json
.agents/skills/verify-database/helpers/control.sh log <run>       # database.lifecycle/v1 events
```

**Raw operator commands** that `control.sh` does not wrap - run `bin/database`
directly against this run's data directory, for example
`bin/database config --config <path> --format=json`. Use the run's
`DATA_DIR` and `PASSWORD_FILE` from `instance.env`; never point a command at a
data directory another run owns.

Prefer stable handles: exact SQL text, MySQL error numbers, JSON field names
from the `database.operator.result/v1`, `database.lifecycle/v1`, and
`database.explain/v1` schemas, and the documented endpoint paths. Do not assert
on human wording - the contract states plainly that human output is not an
automation contract.

## Evidence

Write proofs to this run's `EVIDENCE_DIR`
(`/tmp/verify-database-evidence/<run>/`), outside the repository, so the
repository's litterbug rule stays satisfied and `clean` cannot eat them:

```sh
E=/tmp/verify-database-evidence/<run>
.agents/skills/verify-database/helpers/control.sh sql <run> 'USE shop' 'SELECT id, total FROM orders' | tee $E/orders-after-insert.jsonl
.agents/skills/verify-database/helpers/control.sh catalog <run> > $E/catalog-after-insert.json
.agents/skills/verify-database/helpers/control.sh log <run> > $E/lifecycle.jsonl
```

Proof standards for this repository:

- **Drive the real user path.** Go through the MySQL wire and the operator CLI.
  Never reach into `internal/` packages, and never call a Go test helper as a
  substitute for the product surface.
- **Capture the action and the resulting state**, not only the final read. Keep
  the mutating statement's JSON line as well as the reading one.
- **Verify the side effect.** A write is proven by the on-disk `catalog.json`
  (`control.sh catalog`), or by re-reading it after `control.sh restart`, not by
  the `INSERT` returning `ok`.
- **No mocks.** The server has no external dependency to isolate. Everything is
  the real binary, real sockets, and a real data directory.
- **Test the negative too.** A privilege, constraint, or limit is only proven
  when the forbidden case fails with the documented `error_code`.
- **Record the run identifier and the feature ID** with each artifact, so a
  later reader can tell which instance produced it.
- **Watch what the artifacts hold.** `catalog.json` includes the account grants
  and the admin password hash. That is safe for a disposable run with a
  generated password; never capture it from a real instance.

## Cleanup

```sh
.agents/skills/verify-database/helpers/control.sh clean <run>
```

`clean` sends `SIGTERM` to the exact PID it recorded in `serve.pid`, waits up
to 15 seconds for graceful exit, then deletes `/tmp/verify-database-<run>/`. It
never kills by process name, and it never touches
`/tmp/verify-database-evidence/<run>/`.

Clean every run you started, including failed attempts, so no process holds a
port or a data-directory lock. Confirm with
`ls -d /tmp/verify-database-* 2>/dev/null` - only the evidence root should
remain. If the binary ignored `SIGTERM`, `clean` says so and names the log; do
not escalate to a name-based `pkill`.

`bin/database` is git-ignored build output and can stay.

## Helpers

Both helpers live in `.agents/skills/verify-database/helpers/` and are
executable.

- **`control.sh`** - everything above. Run with no arguments for its usage
  block.
- **`sqlclient/`** - a Go program that opens one MySQL session and runs the
  given statements on it, printing one JSON line per statement. `control.sh
  sql` wraps it; call it directly only for options `control.sh` does not pass
  through:

  ```sh
  go run ./.agents/skills/verify-database/helpers/sqlclient \
    -address 127.0.0.1:60974 -user admin -password-file /tmp/verify-database-<run>/password \
    -database shop -tls -stdin < statements.sql
  ```

  Flags: `-address`, `-user`, `-password`, `-password-file`, `-database`,
  `-tls` (connects with `tls=skip-verify`), `-stdin` (one statement per line;
  blank lines and `--` comments are skipped). Exit `0` when every statement
  succeeded, `1` when any failed, `2` on a usage error.

  Give `go run` the `.agents/` path shown above. That directory starts with a
  dot, so the Go tool excludes it from `./...` patterns and it stays out of
  `make build`, `make test`, and `make messgo`.

## Feature map

[`features/README.md`](features/README.md) is the maintained list of
user-facing features with a driving recipe for each. Read the index before you
drive, then follow the matching feature file. A proof that exercises one
convenient entry point is incomplete when the map lists others; report the
entry points you skipped rather than presenting one as coverage of all.

Keep the map honest with `/maintain-verification-skill` as the product changes.
