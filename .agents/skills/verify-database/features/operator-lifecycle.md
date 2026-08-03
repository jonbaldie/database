# Operator lifecycle

An operator creates a stopped instance with `database init`, runs it with
`database serve`, watches machine-readable lifecycle events on stdout, and stops
it with `SIGINT` or `SIGTERM`. One data directory belongs to exactly one live
`serve` process, and every command reports one
`database.operator.result/v1` terminal record.

## Sub-features

- `lifecycle-version` reports the `database.version/v1` identity.
- `lifecycle-init` creates a data directory from an admin password file or stdin.
- `lifecycle-secret` rejects an inline `--password=` argument.
- `lifecycle-serve` starts the process and emits a `ready` lifecycle event.
- `lifecycle-exclusive` refuses a second `serve` on the same data directory.
- `lifecycle-stop` finishes current work and exits `0` on `SIGTERM`.
- `lifecycle-validate` checks a data directory with `database data validate`.

## How to get to it (user POV)

- Run `bin/database version --format=json`.
- Run `bin/database init <dir> --password-file <file>` or `--password-stdin`.
- Run `bin/database serve --data-directory <dir> --mysql-listen-address=<addr>
  --diagnostics-listen-address=<addr> --format=json`.
- Press `Ctrl-C`, or send `SIGTERM`, to a running `serve`.
- Run `bin/database data validate --data-directory <dir> --result=json`.
- Run `bin/database help` for the command list.

## Driving it with control.sh

Preconditions:

- No instance is running for this run yet.
- `bin/database` exists, or `control.sh start` will build it.

- **Report identity.** Run `bin/database version --format=json`. Stdout is one
  object with `"schema":"database.version/v1"`, `product_version`,
  `build_identity`, `platform`, `data_compatibility`, and
  `mysql_application_compatibility_profile`. Exit code `0`.
- **Create and start an instance.** Run
  `.agents/skills/verify-database/helpers/control.sh start`. It prints `RUN`,
  `DATA_DIR`, `PASSWORD_FILE`, `MYSQL_ADDRESS`, `DIAGNOSTICS_ADDRESS`, and
  `EVIDENCE_DIR`. Keep `RUN`.
- **Confirm initialization.** Run `cat /tmp/verify-database-<run>/init.json`. It
  is one `database.operator.result/v1` object with `"status":"success"`,
  `"exit_class":"success"`, `admin_account`, and `instance_id`. `ls <DATA_DIR>`
  shows `catalog.json` and `instance.json`.
- **Confirm readiness.** Run `control.sh log <run>`. Stdout contains
  `{"schema":"database.lifecycle/v1","state":"ready","event_code":"server.ready",...}`
  with the `diagnostics_address` this run requested.
- **Reject an inline secret.** Run
  `bin/database init /tmp/verify-database-<run>/rejected --password=inline-secret-value --format=json`.
  The result object has `"exit_class":"invalid_input"`, `"exit_code":2`, and the
  diagnostic summary `inline passwords are not supported`. No directory is
  created.
- **Refuse a second owner.** Run
  `bin/database serve --data-directory <DATA_DIR> --mysql-listen-address=127.0.0.1:34567 --format=json`
  while the run is live. The result object has
  `"exit_class":"operation_failed"`, `"exit_code":6`, and the diagnostic summary
  `data directory is already in use`. The live instance keeps serving.
- **Validate stored data.** Run
  `bin/database data validate --data-directory <DATA_DIR> --result=json`. The
  result object reports `data_version` and an `examined` list of checksummed
  files.
- **Stop gracefully.** Run `control.sh stop <run>`. The process exits within 15
  seconds and `control.sh doctor <run>` then fails with
  `no live serve process`.
- **Proof.** Save `init.json`, the lifecycle log, the rejected second-`serve`
  result, and the `data validate` result to
  `/tmp/verify-database-evidence/<run>/operator-lifecycle/`.

## Gotchas

- `bin/database help` lists only `init`, `version`, `serve`, and `config`. The
  `backup`, `restore`, `upgrade`, `data`, and `shutdown` commands are dispatched
  but not listed. Do not conclude from `help` that a command is missing.
- `--help` is not accepted by the operator command family. `bin/database data
  --help` fails with `unsupported data operation "--help"`, exit `2`. Read
  `docs/operator-automation.md` for the input surface instead.
- The password source must hold 12 to 1,024 UTF-8 bytes. One trailing LF or
  CRLF is stripped and every other byte is kept, so a short or space-padded
  password fails or silently differs.
- `serve` emits its successful terminal result only after a graceful stop.
  `ready` is progress, not success; do not treat it as the terminal record.
- A killed `serve` (`SIGKILL`) leaves no terminal result and can leave
  `.running.lock` behind. Always stop with `control.sh stop` or `clean`.
- `--data-directory` wants an absolute path. `init` takes the directory as a
  positional argument; `serve` takes it as the `--data-directory` flag.

