# database

**database** is an experimental single-node relational database server written
in Go. It speaks the MySQL classic wire protocol, so ordinary MySQL client
libraries can connect to it.

It is built for application developers who need to understand and test query
behaviour. Plans and runtime evidence are available through a stable
[query explanation](docs/query-explanation/README.md) contract
(`EXPLAIN` / `EXPLAIN ANALYZE`), not only through logs.

> **v0.1.0 is experimental.** It is not a drop-in MySQL replacement and does
> not claim production readiness. Supported behaviour is the finite surface
> documented under [`docs/`](docs/).

## Quick start

Put `database` on your `PATH` (release binary) or use `./bin/database` after
`make build`. Examples below use `database`.

```sh
# 1. Create a data directory and a password file for the first database account.
mkdir -p ~/database-data
printf 'change-me-now!!' > /tmp/admin-password
chmod 600 /tmp/admin-password

# 2. Initialize a stopped instance (default database account name: admin).
database init \
  --data-directory ~/database-data \
  --initial-account admin \
  --initial-password-file /tmp/admin-password

# 3. Start the server and optional diagnostics listener.
database serve \
  --data-directory ~/database-data \
  --mysql-listen-address=127.0.0.1:3306 \
  --diagnostics-listen-address=127.0.0.1:8080

# 4. Connect with any MySQL client that supports caching_sha2_password.
#    Example DSN for github.com/go-sql-driver/mysql:
#    admin:change-me-now!!@tcp(127.0.0.1:3306)/
```

Passwords must be 12–1024 UTF-8 bytes. Inline `--password=...` is rejected on
purpose. For `init`, use exactly one of `--initial-password-file` or
`--initial-password-stdin`. Online commands use `--password-file` or
`--password-stdin`.

One live `serve` process owns one data directory. A second `serve` on the same
directory fails with “already in use”. Stop with `SIGINT` / `SIGTERM`, or with
`database shutdown`.

## Install

### Prebuilt binaries

Download a release from
[GitHub Releases](https://github.com/jonbaldie/database/releases/tag/v0.1.0):

| Platform | Artifact |
| --- | --- |
| macOS Apple Silicon | `database-0.1.0-darwin-arm64` |
| Linux x86_64 | `database-0.1.0-linux-amd64` |
| Linux arm64 | `database-0.1.0-linux-arm64` |

```sh
curl -fsSL -o database \
  https://github.com/jonbaldie/database/releases/download/v0.1.0/database-0.1.0-darwin-arm64
chmod +x database
./database version
# optional: move onto PATH, e.g. sudo mv database /usr/local/bin/
```

Verify digests with the release `SHA256SUMS` file. Supported runtime baselines
are in [docs/distribution.md](docs/distribution.md).

### From source

Needs a recent Go toolchain (`go.mod` pins the module version; `GOTOOLCHAIN=auto`
fetches a newer toolchain when required).

```sh
git clone https://github.com/jonbaldie/database.git
cd database
make build
./bin/database version
```

### OCI image

The release also ships `database-0.1.0-oci.tar`, a multi-arch OCI image index
(`linux/amd64`, `linux/arm64/v8`). Load it with your preferred OCI runtime, then
run `init` / `serve` inside the container the same way as a native binary.

## How to use it

### Operator commands

| Command | Purpose |
| --- | --- |
| `database init` | Create a stopped instance and initial database account |
| `database serve` | Run the MySQL listener (and optional diagnostics) |
| `database shutdown` | Request a graceful stop of a running instance |
| `database backup create` / `inspect` | Online backup workflows |
| `database restore` | Restore a backup into a new or empty data directory |
| `database upgrade` | Offline data-directory upgrade |
| `database config validate` | Validate server configuration without starting |
| `database data validate` / `inspect` | Offline data checks |
| `database version` | Print product and compatibility identity |

Beyond the [Quick start](#quick-start) `init` / `serve` sequence:

```sh
database version --format=json
# or automation form: database version --result=json

database shutdown \
  --address 127.0.0.1:3306 \
  --account admin \
  --password-file /path/to/password \
  --yes
```

`serve` accepts `--format=json` for lifecycle events on standard output.
Automation-oriented terminal results use `--result=json` and optional
`--progress=json`.

The full operator command family contract is in
[docs/operator-automation.md](docs/operator-automation.md). Server startup
settings are in [docs/server-configuration.md](docs/server-configuration.md).
Session settings are in [docs/session-settings.md](docs/session-settings.md).

### Connect from an application

The server uses MySQL 8.4 classic protocol and `caching_sha2_password`.
Tested client paths include Go’s `github.com/go-sql-driver/mysql` (plaintext and
TLS). Point the driver at the `serve` listen address with the database account
created during `init`.

```go
import (
    "database/sql"
    _ "github.com/go-sql-driver/mysql"
)

db, err := sql.Open("mysql", "admin:change-me-now!!@tcp(127.0.0.1:3306)/")
```

### SQL and query explanation

v0.1 documents a finite MySQL 8.4.11-shaped SQL subset: namespaces, tables,
indexes, constraints, CRUD, joins, subqueries, CTEs, set operations, aggregates,
windows, transactions, catalog metadata, and account administration. See
[docs/mysql-sql-behaviour.md](docs/mysql-sql-behaviour.md).

Inspect plans without guessing:

```sql
EXPLAIN FORMAT=JSON SELECT ...;
EXPLAIN ANALYZE FORMAT=JSON SELECT ...;
EXPLAIN FORMAT=JSON FOR CONNECTION <id>;
```

The stable JSON and tabular shapes are defined in
[docs/query-explanation/README.md](docs/query-explanation/README.md).

### Diagnostics

When `--diagnostics-listen-address` is set, the process exposes:

| Path | Meaning |
| --- | --- |
| `GET /live` | Process is up |
| `GET /ready` | Ready to accept work |
| `GET /metrics` | Operational metrics |

`serve` also emits structured `database.lifecycle/v1` events. On
`SIGINT` / `SIGTERM` it refuses new work, finishes current statements, and rolls
back open transactions before exit.

### Develop and verify this repository

```sh
make build      # bin/database
make test       # go test -race ./...
make quality       # fmt-check, vet, test, build, messgo, vulncheck
make goreportcard  # report-card score; requires A+
```

`make quality` is the project quality gate. It includes pinned `messgo`
full-production analysis on every non-test Go source file and pinned
`govulncheck` dependency analysis. `make goreportcard` uses pinned tools to
calculate the Go Report Card score. Pull requests also enforce an A+ Go Report
Card grade and mutation testing on changed production Go functions with an 80%
minimum score. The [performance acceptance scenario](docs/performance-acceptance.md) is
a release gate for v0.1, not an automated CI benchmark.

## Project docs and policy

| Doc | Contents |
| --- | --- |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |
| [COMPATIBILITY.md](COMPATIBILITY.md) | Compatibility commitments |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Contribution terms |
| [GOVERNANCE.md](GOVERNANCE.md) | Project decision-making |
| [LICENSE](LICENSE) | Apache License 2.0 |
| [docs/operator-automation.md](docs/operator-automation.md) | Operator command contract |
| [docs/server-configuration.md](docs/server-configuration.md) | Startup settings registry |
| [docs/session-settings.md](docs/session-settings.md) | Session settings registry |
| [docs/mysql-sql-behaviour.md](docs/mysql-sql-behaviour.md) | SQL behaviour contract |
| [docs/query-explanation/README.md](docs/query-explanation/README.md) | Query explanation contract |
| [docs/performance-acceptance.md](docs/performance-acceptance.md) | v0.1 performance release gate |
| [docs/distribution.md](docs/distribution.md) | Supported runtimes and artifacts |
| [docs/](docs/) | Full normative contract set |

Licensed under the [Apache License 2.0](LICENSE), including its patent grant.
