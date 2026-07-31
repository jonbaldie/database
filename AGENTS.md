## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

The repository uses the five default triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context project using root `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.

### Cleanup / litterbug rule

Before stopping or handing off, delete disposable artifacts created by your work (temporary test directories, binaries, logs, and generated reports); do not move them to Trash. Never remove pre-existing or unowned files, dirty worktrees, or shared caches without explicit approval. Report every remaining generated artifact over 100 MB with its path and size.
