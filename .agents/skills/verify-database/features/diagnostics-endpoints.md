# Diagnostics endpoints

A running `serve` process exposes an HTTP diagnostics listener with `/live`,
`/ready`, and `/metrics`, so an operator or an orchestrator can tell whether the
process is up, whether it accepts work, and what the execution resources are
doing.

## Sub-features

- `diag-live` answers process liveness.
- `diag-ready` answers service readiness as JSON.
- `diag-metrics` publishes Prometheus text gauges and counters.
- `diag-address` binds only the address the operator requested.
- `diag-absent` omits the listener when no diagnostics address is configured.

## How to get to it (user POV)

- Start `serve` with `--diagnostics-listen-address=<host:port>`, then request
  `http://<host:port>/live`, `/ready`, or `/metrics`.
- Read the `diagnostics_address` field of the `database.lifecycle/v1` ready
  event to learn the bound address.

## Driving it with control.sh

Preconditions:

- One instance is live for this run.
- `control.sh doctor <run>` reports all seven lines.

- **Read the bound address.** Run `control.sh log <run>`. The ready event's
  `diagnostics_address` equals the `DIAGNOSTICS_ADDRESS` that `start` printed.
- **Check liveness.** Run `control.sh diag <run> live`. The request succeeds with
  HTTP `200`.
- **Check readiness.** Run `control.sh diag <run> ready`. Stdout is exactly
  `{"status":"ready"}`.
- **Read metrics.** Run `control.sh diag <run> metrics`. Stdout is Prometheus
  text containing `database_process_ready 1`, `database_server_ready 1`, and the
  resource series `database_execution_memory_bytes`,
  `database_temporary_storage_bytes`, `database_resource_spills_total`,
  `database_resource_cancellations_total`, and
  `database_resource_timeouts_total`.
- **Confirm metrics follow work.** Run a query, for example
  `control.sh sql <run> 'SELECT 1'`, then read `/metrics` again.
  `database_execution_memory_peak_bytes` is greater than zero.
- **Confirm the listener stops with the process.** Run `control.sh stop <run>`,
  then `control.sh diag <run> ready`. `curl` fails to connect.
- **Proof.** Save the ready event, both `/metrics` captures, and the failed
  post-stop request to
  `/tmp/verify-database-evidence/<run>/diagnostics-endpoints/`.

## Gotchas

- The diagnostics listener is optional. A `serve` started without
  `--diagnostics-listen-address` has no HTTP endpoint at all, and its ready
  event carries no bound address. That is correct behavior, not a failure.
- Ports are per run. Never request a hard-coded `8080`; always use this run's
  `DIAGNOSTICS_ADDRESS`.
- `/ready` and `/live` differ. A process can be live and not ready. Assert the
  one the behavior under test needs.
- `control.sh diag` uses `curl -fsS`, so a non-`2xx` status fails the command
  and prints nothing to stdout. Add `-w '%{http_code}'` with your own `curl`
  when the status code is what you are asserting.
- The metrics names are the contract; their values move between runs. Assert
  presence and direction of change, not exact numbers.

