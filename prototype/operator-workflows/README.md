# Prototype: v0.1 operator commands and workflows

> THROWAWAY DECISION PROTOTYPE — this artifact records the operator-visible contract considered while specifying v0.1. It is not implementation guidance or final user documentation.

## Question

Which public commands, required inputs, observable states, and success and failure outcomes define server initialization, startup, shutdown, backup, restore, upgrade, configuration validation, durable-state validation, and version inspection in v0.1?

## Command family

v0.1 distributes one supported `database` executable with this command family:

```text
database init
database serve
database shutdown
database backup create
database backup inspect
database restore
database upgrade
database config validate
database data validate
database data inspect
database version
```

The command family is one public product interface. It does not imply a public contract for internal packages, processes, protocols, file formats, or shared implementation.

## Common behaviour

- Commands produce concise human-readable output by default and a versioned JSON result for automation.
- Human wording may improve during `0.1.x`; machine-readable field meanings remain compatible within that release line.
- Secrets are omitted or redacted in every output mode.
- Long-running work reports its current phase and measurable progress where available, without promising a completion time.
- Progress is separate from the final structured result.
- A successful process outcome means the requested workflow reached its documented terminal state, not merely that it began.
- Stable non-success classes distinguish invalid invocation or configuration, unmet state or compatibility preconditions, connection or authorization failure, validation or corruption failure, unsuccessful operation, and interruption.
- Interactive `shutdown` and `upgrade` require confirmation. Automation must provide an explicit non-interactive acknowledgement rather than relying on a prompt.

## Command matrix

| Command | Required product inputs | Successful outcome | Principal explicit failures |
| --- | --- | --- | --- |
| `database init` | New or empty data directory; initial account name; securely supplied initial password; effective configuration | Creates one stopped, initialized server instance with a new opaque identity and the initial administrative account; creates no application namespace; does not start the server | Target is non-empty or already initialized; invalid account or credential; invalid configuration; unsupported environment; initialization cannot complete cleanly |
| `database serve` | Initialized data directory; effective startup configuration | Runs in the foreground, performs any ordinary crash recovery, becomes ready, accepts normal authenticated work, and remains attached until shutdown or failure | Directory is uninitialized, incompatible, already owned, upgrade-incomplete, or damaged; configuration is invalid; recovery cannot establish trustworthy state; a required endpoint cannot start |
| `database shutdown` | Running server connection; database-account credential; `OPERATIONAL_CONTROL`; confirmation | Requests graceful shutdown, waits by default, and reports completion; process termination signals request the same graceful transition | Cannot connect or authenticate; permission denied; server is already stopping; request not accepted |
| `database backup create` | Running server connection; database-account credential; `OPERATIONAL_CONTROL`; new output path | Writes one complete, validated, transactionally consistent full-server backup while normal reads and writes continue | Existing output; another backup is active; connection, authentication, or authorization failure; source becomes unavailable; destination cannot accept the complete artifact; cancellation |
| `database backup inspect` | Backup artifact | Reports completeness, integrity status, creation time, source instance identity, source server version, and compatibility without exposing durable contents or credentials | Artifact is incomplete, unreadable, damaged, or from an unsupported format |
| `database restore` | Complete compatible backup; new or empty target data directory | Creates one stopped complete server instance containing all backed-up durable state; assigns the restored instance an identity and records its source identity; does not merge, start, or silently upgrade it | Existing or non-empty target; incomplete, damaged, or incompatible backup; restoration cannot complete cleanly |
| `database upgrade` | Offline data directory; completed full-server backup covering its pre-upgrade state; confirmation | Performs an explicit forward transition to the running command's `0.1.x` data version and reports the new compatibility state | Server still owns the directory; missing or mismatched backup; unsupported source or target; downgrade requested; validation or compatibility preflight fails; upgrade cannot continue safely |
| `database config validate` | Configuration file and any supplied overrides | Reports a valid effective configuration, its redacted values, and their sources without changing durable state | Unknown, invalid, conflicting, or unsupported setting |
| `database data validate` | Offline data directory | Performs read-only comprehensive validation within the documented corruption-detection guarantee and reports structured findings | Directory is active, incompatible, unreadable, or damaged; validation is interrupted or cannot complete |
| `database data inspect` | Offline data directory | Reports instance identity, data version, creating and compatible server versions, and whether recovery or upgrade is required, without exposing application contents or internal file structure | Directory is active, uninitialized, unreadable, or not recognizable as a supported data directory |
| `database version` | None | Reports product version, build identity, supported platform, supported data and backup compatibility ranges, and named MySQL application compatibility profile | The executable cannot report a valid build identity |

## Initialization and the initial account

Initialization is explicit and never happens as a side effect of startup.

The initial account name has no default. Its credential must be supplied through a documented secret-safe input rather than appearing in ordinary diagnostics. The account begins with `ACCOUNT_MANAGER`, `NAMESPACE_MANAGER`, `OPERATIONAL_OBSERVATION`, and `OPERATIONAL_CONTROL`. It receives no namespace grants because initialization creates no application namespace.

Initialization never overwrites a target and has no force mode. Success yields one usable initialized instance; failure never leaves a directory that can be mistaken for successful initialization and reports whether any operator cleanup is required.

## Server lifecycle

The public lifecycle states are:

```text
uninitialized -> stopped -> starting -> recovering? -> ready -> stopping -> stopped
                                      \-> failed
stopped -> upgrade-incomplete -> stopped
any durable corruption detected -> failed and unavailable for normal service
```

`database serve` is foreground-only. Service managers and container runtimes own background operation, restart policy, and log retention. v0.1 promises no daemon mode or PID-file workflow.

Startup automatically performs ordinary crash recovery before readiness. It never initializes or upgrades durable state. The observable server states are `starting`, `recovering`, `ready`, `stopping`, and `failed`.

Graceful shutdown stops sessions from beginning new work, lets each current statement finish, closes sessions, and rolls back remaining uncommitted transactions. There is no remote forced-shutdown command. Abrupt termination remains a crash and invokes automatic recovery on the next start.

## Online and offline authority

`shutdown` and `backup create` act through a running server. They authenticate as database accounts and use the established authorization model; they introduce no separate operator credential system.

`init`, `restore`, `upgrade`, `data validate`, and `data inspect` act on local offline state. They require exclusive access to their target and use the operator's access to that artifact or directory rather than database credentials.

`config validate`, `backup inspect`, and `version` do not require a running or initialized server.

## Backup and restore

Only a fully written and validated artifact is a backup. Cancellation or loss of the invoking command aborts the operation; incomplete output is visibly unusable and may be removed safely. Retrying begins a fresh backup. Only one backup may be active for a server.

The output path belongs to the environment running `database backup create`, allowing the operator command to protect a server without treating a live data-directory copy as a backup.

A backup is opaque and sensitive because it contains all durable server-owned state, including accounts and credentials. v0.1 provides no built-in backup encryption or key management. Storage protection, transfer protection beyond the documented command connection, retention, and access control remain operator responsibilities.

Restore is complete-server only. It never merges, overwrites, selectively restores, starts the server, or silently upgrades an older backup. Configuration and externally managed secrets remain outside the artifact. When an older restored data version requires upgrade, inspection and startup say so explicitly and the separate upgrade workflow remains mandatory.

## Upgrade interruption

Upgrade is offline, explicit, forward-only, and targets the data version associated with the running command. It verifies compatibility and the required completed backup before changing durable state.

If interrupted, the directory cannot be mistaken for an ordinary startable instance. It remains visibly upgrade-incomplete; normal startup refuses it; rerunning the same target-version upgrade resumes or safely completes the transition. v0.1 provides no downgrade or automatic rollback workflow.

## Validation and inspection

Configuration validation evaluates the same effective setting precedence used by startup. It identifies the source of each effective value and redacts secrets. Environmental conditions such as endpoint availability are checked again at startup and are not guaranteed by offline validation.

Durable-state validation is offline, read-only, and non-repairing. Any detected corruption is a failed outcome. A clean result means the documented checks completed; it is not a claim that every possible storage or hardware fault is detectable.

Data and backup inspection expose compatibility and lifecycle metadata only. They do not establish a public durable-file or backup representation.

## Public output boundary

Machine-readable results carry a schema version, command identity, terminal status, outcome class, and command-specific fields. Human progress and message wording are not compatibility surfaces. Structured logs and established operational introspection expose live work; v0.1 creates no separate permanent operator-command audit history.

## Deliberately unspecified mechanics

This contract does not choose command-framework libraries, process boundaries, control channels, backup encoding or compression, migration algorithms, validation algorithms, durable markers, filesystem layout, IPC, or package decomposition.

## Verdict

Accepted: v0.1 exposes the command family and operator-visible workflow contract above.
