# database

**database** is an experimental relational database for application developers
who want to inspect and test query behaviour with familiar MySQL clients and
SQL.

## Why try it?

- **See what a query does.** A query explanation shows the selected work,
  estimates, runtime evidence, resource use, and active progress. It is
  available as stable JSON or as a table.
- **Use a familiar client.** The server uses the MySQL classic wire
  protocol. The project tests real Go, PHP, Node.js, Python, and Java clients.
- **Know the limits before you depend on it.** The project has a finite SQL
  contract, named client tests, and public test evidence from product
  interfaces.

> **database is experimental.** Do not use it as a production database. It is a
> single-node server and is not a drop-in MySQL replacement. The supported
> behaviour is the finite surface under [`docs/`](docs/).

## Try it

This trial starts one local instance. It uses the Go MySQL driver to create
data, run a prepared query, and get a query explanation.

The `database` executable is a server and operator command. It does not include
a SQL client.

### 1. Install one binary

Download the file for your system from the
[latest GitHub Release](https://github.com/jonbaldie/database/releases/latest):

| System | File |
| --- | --- |
| Apple Silicon macOS | `database-<version>-darwin-arm64` |
| x86-64 Linux | `database-<version>-linux-amd64` |
| ARM64 Linux | `database-<version>-linux-arm64` |

Rename the file to `database`, make it executable, and put it on your `PATH`.

```sh
chmod +x database
./database version
```

### 2. Initialize and start the server

Use a separate terminal for the server. This password is only for the local
trial.

```sh
mkdir -p database-trial/data
printf 'change-me-now!!' > database-trial/admin-password
chmod 600 database-trial/admin-password

./database init \
  --data-directory "$PWD/database-trial/data" \
  --initial-account admin \
  --initial-password-file "$PWD/database-trial/admin-password"

./database serve \
  --data-directory "$PWD/database-trial/data" \
  --mysql-listen-address=127.0.0.1:3306 \
  --diagnostics-listen-address=127.0.0.1:8080
```

One live `serve` process owns one data directory. A second process cannot use
the same directory.

### 3. Run a query and inspect it

In another terminal, create a small Go program:

```sh
mkdir database-client
cd database-client
go mod init database-client
go get github.com/go-sql-driver/mysql@v1.9.3
```

Save this file as `main.go`:

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	ctx := context.Background()
	db, err := sql.Open("mysql", "admin:change-me-now!!@tcp(127.0.0.1:3306)/")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	statements := []string{
		"CREATE DATABASE trial",
		"USE trial",
		"CREATE TABLE tasks (id BIGINT PRIMARY KEY, title VARCHAR(100) NOT NULL, priority INT NOT NULL)",
		"INSERT INTO tasks VALUES (1, 'read the plan', 1), (2, 'test query behaviour', 2)",
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			log.Fatal(err)
		}
	}

	rows, err := conn.QueryContext(ctx,
		"SELECT id, title FROM tasks WHERE priority >= ? ORDER BY id", 2)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%d: %s\n", id, title)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		log.Fatal(err)
	}

	var explanation string
	err = conn.QueryRowContext(ctx,
		"EXPLAIN ANALYZE FORMAT=JSON SELECT id, title FROM tasks WHERE priority >= 2 ORDER BY id",
	).Scan(&explanation)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(explanation)
}
```

Run it:

```sh
go run .
```

The first line is the query result:

```text
2: test query behaviour
```

The next line is the query explanation JSON. It includes the format version,
the physical operator tree, estimates, actual row counts, elapsed time, memory,
reads, and warnings. See the
[checked example](docs/query-explanation/examples/analyze.json) and the
[query explanation contract](docs/query-explanation/README.md).

Stop the server with `Ctrl-C`. The server finishes current statements and
rolls back open transactions before it exits.

## Is it a fit?

Try database if you want to:

- inspect query behaviour in application tests;
- test a new local relational database through a supported MySQL client;
- inspect planned, completed, or active query work;
- test transactions, locks, limits, and failure results; or
- examine a small database server written in Go.

Do not use database if you need:

- a production service;
- complete MySQL compatibility;
- more than one server node;
- an unsupported operating system or processor; or
- an undocumented SQL or protocol feature.

## Supported application surface

### MySQL clients

The tested client profiles use:

- Go `go-sql-driver/mysql`;
- PHP PDO and mysqli;
- Node.js `mysql2`;
- Python Connector/Python;
- Java Connector/J; and
- the MySQL command-line client.

The project tests exact client versions and connection forms. This list does
not mean that every MySQL client or MySQL feature works. See the
[compatibility evidence](docs/compatibility-evidence.md).

### SQL

The SQL contract has a finite subset that is based on MySQL 8.4.11. It includes:

- database namespaces, tables, indexes, and constraints;
- insert, replace, update, delete, and select;
- joins, subqueries, common table expressions, and set operations;
- aggregates and windows;
- transactions, savepoints, row locks, and read-only transactions;
- catalog metadata through supported `SHOW` statements and
  `information_schema` views; and
- database accounts and grants.

The server returns explicit errors for unsupported input. See the complete
[MySQL SQL behaviour contract](docs/mysql-sql-behaviour.md).

### Query explanation

Use these forms to inspect query behaviour:

```sql
EXPLAIN FORMAT=JSON SELECT ...;
EXPLAIN ANALYZE FORMAT=JSON SELECT ...;
EXPLAIN FORMAT=JSON FOR CONNECTION <connection_id>;
```

`EXPLAIN` shows planned work without execution. `EXPLAIN ANALYZE` runs a
non-locking `SELECT` and adds complete runtime evidence. `EXPLAIN FOR
CONNECTION` gives a non-blocking partial view of an active query.

The JSON document has a versioned contract. The table form has a stable column
set. The contract keeps the format stable within version 1. It does not promise
that the selected plan will never change.

## Operate one local instance

The same executable supports:

- graceful shutdown;
- online backup creation and backup inspection;
- restore into a new or empty data directory;
- offline upgrade;
- configuration validation;
- data validation and inspection; and
- version reports.

Automation can request versioned JSON results and progress. The diagnostics
listener supplies `/live`, `/ready`, and `/metrics`.

See the [operator automation contract](docs/operator-automation.md), the
[server configuration registry](docs/server-configuration.md), and the
[session settings registry](docs/session-settings.md).

## Other installation methods

### Build from source

To build from source, use the Go version in `go.mod`. `GOTOOLCHAIN=auto`
downloads a newer toolchain when a quality tool needs it.

```sh
git clone https://github.com/jonbaldie/database.git
cd database
make build
./bin/database version
```

### OCI image

Each release includes a `database-<version>-oci.tar` artifact containing
`linux/amd64` and `linux/arm64/v8` images. Load it with an OCI runtime, then use
the same `init` and `serve` commands.

See the complete [distribution evidence](docs/distribution.md).

## Help and contribution

The project does not yet publish a separate support channel. Use
[GitHub Issues](https://github.com/jonbaldie/database/issues) to report a
defect or request a feature. Include the database version, the client and its
version, the SQL statement, and the complete MySQL error number and SQLSTATE
when they are available.

Before you contribute, read [CONTRIBUTING.md](CONTRIBUTING.md) and
[GOVERNANCE.md](GOVERNANCE.md).

Run the project quality checks before you send a change:

```sh
make quality
```

## Evidence and policy

| Document | Contents |
| --- | --- |
| [docs/conformance-evidence.md](docs/conformance-evidence.md) | Public test evidence for each product surface |
| [docs/compatibility-evidence.md](docs/compatibility-evidence.md) | Tested MySQL clients and journeys |
| [docs/mysql-sql-behaviour.md](docs/mysql-sql-behaviour.md) | Supported SQL behaviour |
| [docs/query-explanation/README.md](docs/query-explanation/README.md) | Query explanation formats and meanings |
| [docs/operator-automation.md](docs/operator-automation.md) | Operator command inputs and results |
| [docs/distribution.md](docs/distribution.md) | Supported systems and release artifacts |
| [docs/performance-acceptance.md](docs/performance-acceptance.md) | Experimental performance release gate |
| [COMPATIBILITY.md](COMPATIBILITY.md) | Public compatibility policy |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |

database uses the [Apache License 2.0](LICENSE), including its patent grant.
