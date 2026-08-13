Only report to me in ASD-STE100 Simplified Technical English.

## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

The repository uses the five default triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context project using root `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.

### Verification

Use the `verify-database` skill to launch the server and prove user-visible behavior with evidence. Read `.agents/skills/verify-database/features/README.md` before you drive the product.

### Cleanup / litterbug rule

Before stopping or handing off, delete disposable artifacts created by your work (temporary test directories, binaries, logs, and generated reports); do not move them to Trash. Never remove pre-existing or unowned files, dirty worktrees, or shared caches without explicit approval. Report every remaining generated artifact over 100 MB with its path and size.

## Cursor Cloud specific instructions

This is a single-binary Go database server (MySQL wire protocol). All standard commands live in the `Makefile` and `README.md`; use those rather than duplicating them here.

- Toolchain: `go.mod` pins `go 1.26.5`, which also meets the `messgo` quality tool requirement of `go >= 1.26`. With `GOTOOLCHAIN=auto`, Go downloads that toolchain when required. The first `make messgo` or `make quality` run also needs network access to fetch `messgo@v0.2.0`.
- Quality gate: `make quality` = `fmt-check vet test build messgo vulncheck`. `make test` runs with `-race`; the `test/qualitygate` suite dominates runtime (~80s), so the full `make test` takes ~90s.
- Running the server (see `README.md`): first `bin/database init <data-dir> --password-file <file>` (or `--password-stdin`; an inline `--password=` is intentionally rejected), then `bin/database serve --data-directory <data-dir> --mysql-listen-address=... --diagnostics-listen-address=...`. A data directory is owned exclusively by one live `serve` process; a second `serve` on the same directory fails with "already in use".
- Diagnostics listener exposes `/live`, `/ready`, and `/metrics`. `serve` emits `database.lifecycle/v1` JSON to stdout and shuts down gracefully on `SIGINT`/`SIGTERM`.
- No `mysql` CLI is installed. Connect with a MySQL client library (the blackbox tests and the server use `caching_sha2_password`; `github.com/go-sql-driver/mysql` works both over plaintext and with `tls=skip-verify`).
