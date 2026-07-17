# database

This repository is building a transparent, MySQL-compatible relational database server in Go.

## Project policy

database is licensed under the [Apache License 2.0](LICENSE), including its
patent grant. See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution terms,
[GOVERNANCE.md](GOVERNANCE.md) for project decision-making, and
[COMPATIBILITY.md](COMPATIBILITY.md) for release and public compatibility
commitments.

The executable delivery spine currently provides the public process seams used by black-box verification:

```sh
make build
bin/database version
bin/database version --format=json
bin/database serve --format=json --diagnostics-address=127.0.0.1:8080 --state-file=.database-state
```

`database version --format=json` emits the versioned `database.version/v1` identity. The `serve` command is the process seam: it reports `database.lifecycle/v1` readiness, exposes the initial `/live`, `/ready`, and `/metrics` diagnostics probes, handles graceful termination, and leaves a state marker that makes an unclean restart observable. SQL, storage, authentication, and complete operator workflows are implemented by their respective tickets.

Run the repository verification with `make quality`. Pull requests also enforce mutation testing on changed production Go functions with an 80% minimum score.
