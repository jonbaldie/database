# MySQL application-driver compatibility evidence

This document records the bounded compatibility profile verified for issue
[#69](https://github.com/jonbaldie/database/issues/69). The baseline is MySQL
8.4.11. The probes use real network connections to the database server and
exercise connection, authentication, schema selection, text SQL, prepared
SQL, session state, result metadata, and unknown-table error identity.

## Pinned clients

| Client | Version | Transport/profile |
|---|---:|---|
| Go `go-sql-driver/mysql` | v1.9.3 | plaintext, `database/sql`, text and server-prepared queries |
| PHP PDO and mysqli | PHP 8.4.1 | TLS, native prepares |
| Node `mysql2` | v3.23.2 | TLS, text and prepared queries |
| Python Connector/Python | v9.4.0 | TLS, pure-Python text and prepared cursor |
| Java Connector/J | v9.4.0 | TLS, server-prepared statements |
| MySQL CLI | 9.7.1 | TLS, batch query |

The first five rows are the full-conformance driver set. The CLI is an
additional tooling smoke profile.

## Reproduction

The Go profile is always-on:

```sh
go test ./test/blackbox -run '^TestGoDriverCompatibilityProfile$' -count=1
```

The external profile is opt-in because its language dependencies are not
vendored. Set `DATABASE_COMPATIBILITY_DRIVERS=1` and provide the pinned
`mysql2`, Connector/Python, and Connector/J installations:

```sh
PATH="/opt/homebrew/opt/mysql-client/bin:$PATH" \
NODE_PATH=/path/to/mysql2/node_modules \
PYTHONPATH=/path/to/mysql-connector-python \
MYSQL_CONNECTOR_JAR=/path/to/mysql-connector-j-9.4.0.jar \
DATABASE_COMPATIBILITY_DRIVERS=1 \
go test ./test/blackbox -run '^TestExternalDriverCompatibilityProfile$' -count=1 -v
```

The external test fails when a required executable, extension, or JAR is
missing. It does not replace the clients with mocks.

## Observed scenarios

- plaintext Go and TLS external connections authenticate with
  `caching_sha2_password`;
- `VERSION()`, schema creation/selection, and session time-zone changes work;
- text queries return column names and typed values;
- server-prepared queries preserve string, integer, and `NULL` values;
- unknown tables preserve MySQL error 1146 and SQLSTATE `42S02`;
- Connector/J's startup server-variable probe and client session setup work;
- the mysql CLI can connect and execute a batch query.

The profile does not claim support for optional capabilities that the server
does not advertise, including compression and multi-statement execution.
