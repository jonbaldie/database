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

- Toolchain: `go.mod` pins `go 1.26.6`, which also meets the `messgo` quality tool requirement of `go >= 1.26`. With `GOTOOLCHAIN=auto`, Go downloads that toolchain when required. The first `make messgo` or `make quality` run also needs network access to fetch `messgo@v0.2.0`.
- Quality gate: `make quality` = `fmt-check vet test build messgo vulncheck`. `make test` runs with `-race`; the `test/qualitygate` suite dominates runtime (~80s), so the full `make test` takes ~90s.
- Running the server (see `README.md`): first `bin/database init <data-dir> --password-file <file>` (or `--password-stdin`; an inline `--password=` is intentionally rejected), then `bin/database serve --data-directory <data-dir> --mysql-listen-address=... --diagnostics-listen-address=...`. A data directory is owned exclusively by one live `serve` process; a second `serve` on the same directory fails with "already in use".
- Diagnostics listener exposes `/live`, `/ready`, and `/metrics`. `serve` emits `database.lifecycle/v1` JSON to stdout and shuts down gracefully on `SIGINT`/`SIGTERM`.
- No `mysql` CLI is installed. Connect with a MySQL client library (the blackbox tests and the server use `caching_sha2_password`; `github.com/go-sql-driver/mysql` works both over plaintext and with `tls=skip-verify`).

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

<!-- BEGIN BEADS CODEX SETUP: generated by bd setup codex -->
## Beads Issue Tracker

Use Beads (`bd`) for durable task tracking in repositories that include it. Use the `beads` skill at `.agents/skills/beads/SKILL.md` (project install) or `~/.agents/skills/beads/SKILL.md` (global install) for Beads workflow guidance, then use the `bd` CLI for issue operations.

### Quick Reference

```bash
bd ready                # Find available work
bd show <id>            # View issue details
bd update <id> --claim  # Claim work
bd close <id>           # Complete work
bd prime                # Refresh Beads context
```

### Rules

- Use `bd` for all task tracking; do not create markdown TODO lists.
- Run `bd prime` when Beads context is missing or stale. Codex 0.129.0+ can load Beads context automatically through native hooks; use `/hooks` to inspect or toggle them.
- Keep persistent project memory in Beads via `bd remember`; do not create ad hoc memory files.

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.
<!-- END BEADS CODEX SETUP -->
