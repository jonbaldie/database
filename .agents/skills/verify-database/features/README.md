# Database verification features

Read the files for every affected public entry point.

| Feature | Driver | Proof |
| --- | --- | --- |
| [Lifecycle and diagnostics](lifecycle.md) | Operator CLI and HTTP process tests | Version, readiness, stop, and diagnostics responses |
| [SQL and indexes](sql.md) | MySQL wire process tests | Exact rows, index behavior, and durable client state |
| [Query explanation](explanation.md) | MySQL wire process tests | Planned, analyzed, and active query evidence |
| [Account administration](accounts.md) | MySQL wire process tests | Durable account changes and last-manager protection |

These are initial entries, not a complete conformance inventory. For another command or SQL feature, read `docs/conformance-evidence.md` and select its public process tests. Add a map entry when the public workflow is not covered here.
