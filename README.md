# database

This repository is building a transparent relational database server in Go with
a documented finite [MySQL protocol and SQL surface](docs/mysql-sql-behaviour.md).

## Project policy

database is licensed under the [Apache License 2.0](LICENSE), including its
patent grant. See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution terms,
[GOVERNANCE.md](GOVERNANCE.md) for project decision-making, and
[COMPATIBILITY.md](COMPATIBILITY.md) for release and public compatibility
commitments.

The versioned [query explanation contract](docs/query-explanation/README.md)
defines the stable JSON and tabular explanation formats that implementation
tickets must expose.

The closed [server configuration registry](docs/server-configuration.md)
defines every startup setting, exact input form, precedence rule, default, and
validation boundary.

The [session settings registry](docs/session-settings.md) defines every
published MySQL session setting, its default, scope, mutability, and reset
behavior.

The [operator automation contract](docs/operator-automation.md) defines the
exact command inputs, secret boundary, progress records, terminal results,
diagnostics, exit classes, and compatibility rules for automating every
supported operator workflow.

The [performance acceptance scenario](docs/performance-acceptance.md) defines
the versioned non-domain corpus, application-visible gates, reference
environment, measurement rules, and release evidence for v0.1. It is a release
gate, not an automated CI benchmark or a universal deployment guarantee.

The executable delivery spine currently provides the public process seams used
by black-box verification. These examples show the current partial delivery
syntax; the complete, normative automation syntax is in the
[operator automation contract](docs/operator-automation.md):

```sh
make build
bin/database version
bin/database version --format=json
bin/database init /absolute/path/to/data --password-file /absolute/path/to/admin-password
bin/database serve --data-directory /absolute/path/to/data --mysql-listen-address=127.0.0.1:3306 --format=json --diagnostics-listen-address=127.0.0.1:8080
```

`database version --format=json` emits the versioned `database.version/v1` identity. The `serve` command is the process seam: it exclusively owns one initialized data directory, reports `database.lifecycle/v1` readiness, exposes `/live`, `/ready`, and `/metrics`, and handles `SIGINT` or `SIGTERM` by refusing new work, finishing current statements, and rolling back open transactions. SQL, storage, authentication, and complete operator workflows are implemented by their respective tickets.

Run the repository verification with `make quality`. It includes a pinned
`messgo` full-production analysis at the upstream default design and code-size
levels, applied to every non-test Go source file, not just changed files.
Pull requests also enforce mutation testing on changed production Go functions
with an 80% minimum score.
