---
name: verify-session-protocol
description: Check selected session admission, handshake, cancellation, prepared state, settings, grants, diagnostics, and live explanation boundaries when changing the covered MySQL session functions. This is a partial subsystem check.
---

# Verify session protocol boundaries

An active statement must have a cancellation channel. When the statement returns, its active state and explanation must disappear. Capacity checks, prepared reset, session limits, grant removal, diagnostics ownership, and explanation cleanup must preserve their stated rules.

Run from the repository root.

```sh
.agents/skills/verify-session-protocol/run
```

The command needs Python 3, Java 11 or later, and the Go toolchain in go.mod. It uses the checksum-pinned TLC 1.7.4 jar installed by verify-wal-atomicity. Run that skill first if the jar is absent. Set TLA2TOOLS to a jar with the same checksum to use another location. Go and TLC use at most two processors.

Read each model result with `/statechart`. Each model has a matching config and View.js file in this folder. Broken configs are intentional negative controls. They must violate the named rule. The generated chart reports are in docs/formal/session-protocol.

## Source coverage and sync

Last reviewed source is fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1. Before each run, inspect later commits and uncommitted changes to the functions below. Confirm that their transition boundaries still match. Move the sync commit forward only after the models and probes match the changed code.

| History | Model | Source boundary | Rule and bound |
| --- | --- | --- | --- |
| 14 | Admission | internal/mysql/server.go, register, admitSession, unregister | One pending slot and one session slot. The second session cannot exceed capacity. |
| 15 | Handshake | internal/mysql/authentication.go, handshakeCapabilities | All eight subsets of the three required capability flags. Acceptance requires all three. |
| 16 | Cancellation | internal/mysql/connection.go, conversationControl.cancelStatement and takeWatch | Two cancellation requests close one watch once. |
| 19 | Prepared | internal/mysql/server.go, sendLongData, resetPrepared, clearPreparedLongData, closePrepared | One statement, one parameter, a two-byte chunk. Reset releases all buffered bytes. |
| 20 | Settings | internal/mysql/session_settings.go, setByteLimit | Initial limits 1 and 2, requested values 0 through 3. Accepted assignments stay within ceiling 2. The Go probe also checks -1. |
| 21 | AccountGrant | internal/mysql/account_administration.go, grantChange | One read grant. Repeated grant does not duplicate access. Revocation removes access. |
| 23 | Diagnostics | internal/mysql/warnings.go, replaceDiagnostics | One diagnostic. A later caller write cannot change the stored diagnostic. |
| 24 | LiveExplanation | internal/mysql/live_explanation.go, begin and snapshot | Two entries for one connection ID. Cleanup for the old entry cannot remove the new entry. |
| 14, 15, 16, 24 | ConnectExecute | internal/mysql/connection.go, authenticate and runStatement | One supported text statement. RunningHasWatch checks cancellation availability. CompletionHidesState checks active-state cleanup. |

The eight boundary models expose selected straight-line histories. They do not enumerate arbitrary calls or concurrent schedules. ConnectExecute is the user-flow abstraction. Authentication success and failure are external outcomes in that chart. The chart assumes a supported text statement can be planned. It does not verify a full password exchange or query result transport.

## Current and broken-source checks

The base checks run nine current models and ten broken configs. The history extension below adds seven current models, fifteen broken configs, and sixteen source faults, for totals of sixteen current models, twenty-five broken configs, and twenty-six source faults. It loads session_test.go.txt into the real mysql package through a temporary Go overlay. It then runs ten separate source overlays. Each negative run must fail at its named behavioral assertion. A compile error does not count as a caught mutation.

The overlays remove the active cancellation channel, retain the running flag after completion, remove session capacity, remove one required handshake flag, close a watch twice, retain prepared bytes, remove the byte ceiling, omit grant removal, retain a diagnostic slice alias, and remove explanation ownership before cleanup. No production file is changed.

The execution probe calls runStatement over a local net.Pipe. It checks active state, cancellation delivery, explanation visibility, and cleanup on normal return. It closes both pipe ends after the probe. The remaining probes call the covered real functions directly.

## Interpret and maintain

A current-model or current-source failure stops the run. Trace the observed transition and reproduce any model failure against real code before reporting a product defect. Keep every failing rule. If source behavior changed intentionally, update the model and source probe together, demonstrate the negative check, and rerun this skill and `/statechart`. Use `/maintain-formal-verification-skill` after covered source changes without these checks.

The boundary models above remain available. The history models below extend the named sequences. Arbitrary transport failures, cryptographic correctness, arbitrary concurrent schedules, KILL authorization, prepared ID wrap and all parameter encodings, every setting, last-manager protection, resource failure cleanup, and live metric concurrency remain outside these checks.

## Session history sequences

The same command also runs run-histories. The data shape is a finite phase and the state that each action owns. Read each history model and its current and broken configs with `/statechart`, using its matching View.js file. history-model-index.json lists the negative controls. All new properties have named broken-model and real-source checks.

| History | Model | Action-to-source map | Bound and property |
| --- | --- | --- | --- |
| 15 | AuthenticationHistory | Greeting maps to conversation.authenticate. UpgradeTLS maps to authenticationExchange.upgradeTLS. ReadResponse and ValidateToken map to handshakeResponse and authenticator.validate. RequestFullPassword, SendPublicKey, ReadPassword, and VerifyPassword map to fullPassword, rsaPassword, and authenticate in authentication.go. | One connection. TLS or RSA, valid or invalid token, valid or invalid full password, valid or invalid RSA key request. Acceptance requires verified credentials. The RSA key stage requires the request byte. |
| 14 | AdmissionDrain | register, admitSession, beginStatement, endStatement, and closeGracefully in server.go. | One admitted connection and one active statement. Drain rejects new work and waits for the active statement before closing sessions. |
| 16 | CommandHistory | watchStatement, takeWatch, acceptCommand, and readPendingCommand in connection.go; IdleTimeouts.await in idle_timeout.go. | One active statement, one next command, one open transaction. Buffered input survives return. Expiry rolls back. Revocation prevents another dispatch. |
| 19 | PreparedHistory | preparedPreparation.allocate, preparedExecution.preparedValues, preparedLifecycle.sendLongData/resetPrepared/resetConnection in server.go. | One allocated statement and one parameter. Two executions share types. Reset clears long data. Connection reset releases the statement and slot. |
| 20 | SetAssignmentHistory | databaseSelector.use in server.go; applySessionAssignments in session_settings.go. | Select one database, apply one valid assignment, fail an unknown assignment, and stop before the last assignment. The successful prefix and selected database persist. |
| 21 | AccountHistory | createAccount, changeAccountPassword, setAccountLock, and dropAccount in account_administration.go; revokeAccount in server.go; accountPasswordHash in authentication.go. | One account and one existing session. Create, change, lock, unlock, reconnect, and drop. Locked or absent accounts cannot authenticate. Disabling revokes the session. |
| 23 | WarningHistory | startStatementDiagnostics and finishStatementDiagnostics in statement_policy.go; addDiagnostic and showDiagnostics in warnings.go; resetConnection in server.go. | One warning, two SHOW calls, an ordinary statement, one failure, and connection reset. SHOW retains warnings. Ordinary execution and reset clear them. |

The authentication probe runs the real conversation.authenticate over net.Pipe. It reads the greeting, sends the response, completes either a real TLS handshake or RSA encrypted password exchange, and reads OK or error. It checks correct passwords, wrong full passwords, invalid initial tokens, and plaintext supplied without TLS. The certificate is local to the test. This checks protocol ordering and source authentication guards. It does not prove TLS, RSA, hashing, or randomness.

Account probes use a real catalog to create and change the account, then check lock/unlock/drop and an existing session revocation flag. The modeled Reconnect step means a replacement session begins with a fresh revocation flag. Unlocking does not restore a revoked connection. The probe models an active existing statement and checks that revocation becomes visible. It does not complete that statement on a live network.

The drain probe starts the real closeGracefully call while one statement is active. It checks rejection after listener close, retains the existing connection until endStatement, then checks connection closure and accounting cleanup. A short bounded observation window checks that close has not happened. It does not enumerate every goroutine schedule.

The command probe sends the next command through the real read-ahead goroutine, checks cancellation and preserved input, then expires a transaction through the real timer path and checks rollback. A preloaded next command checks rejection after revocation. Prepared probes allocate a real SELECT with one placeholder, decode and execute values 7 and 9 with supplied then cached types, append/reset long data, and reset the connection. This checks the selected decode path, not every type or packet.

The source faults remove the initial-token or full-password guard, accept an invalid RSA request, omit drain waiting, lose read-ahead input, omit idle rollback, bypass revocation, lose cached types, omit prepared reset cleanup, bypass locked-account rejection, omit account revocation, clear SHOW warnings, retain ordinary warnings, omit reset diagnostics cleanup, continue SET after error, or omit database selection. Each must fail its named assertion. Current source is checked again after the faults. The runner never changes product files.

These are bounded histories, not complete protocol or SQL proofs. Successful filesystem/catalog operations and supported statements are assumed in the model. The source probes exercise one explicit sequence for each branch listed above. Charts and reports are in docs/formal/session-protocol/histories. They preserve cross-label transitions and omit only same-label steps.
