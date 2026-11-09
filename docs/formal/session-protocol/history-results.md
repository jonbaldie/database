# Session history sequence checks

Source baseline: fc78c5417c39374ec22f9c2f4c3ea1452f7d16e1.

Run `.agents/skills/verify-session-protocol/run` from the repository root. Its `run-histories` extension checks seven current sequence models, fifteen broken configs, positive real-source probes, sixteen source faults, and restored-current probes. All passed. Each broken config must violate its named invariant. Each source fault must fail its named behavioral assertion. A compiler error does not satisfy a negative check.

The combined command also retains nine boundary models, ten broken configs, and ten source faults. Combined totals are sixteen current models, twenty-five broken configs, and twenty-six source faults. These counts describe bounded checks, not a percentage of product coverage.

| History | Sequence model | Distinct current states | Sequence |
| --- | --- | --- | --- |
| 14 | AdmissionDrain | 6 | Pending, admitted, executing, draining, statement finished, closed. |
| 15 | AuthenticationHistory | 96 | Greeting, optional TLS, response, token validation, full-password request, TLS plaintext or RSA request/key/encrypted response, password verification, accepted or rejected. |
| 16 | CommandHistory | 7 | Executing, read-ahead buffered, idle, then dispatch, timeout rollback, or revocation and close. |
| 19 | PreparedHistory | 7 | Allocate, execute with types, execute with cached types, append long data, reset statement, reset connection. |
| 20 | SetAssignmentHistory | 5 | Select database, begin SET, apply first assignment, fail second assignment, retain successful prefix. |
| 21 | AccountHistory | 7 | Create, change password, lock, unlock, replace revoked session, drop. |
| 23 | WarningHistory | 7 | Add warning, SHOW twice, ordinary statement, failure retained, connection reset. |

The skill gives the source function for each action. `history-model-index.json` gives the broken config and invariant pairs. The checks below tie each new property to a real source mutation.

| Property | Broken model fault | Real-source fault caught |
| --- | --- | --- |
| AcceptedPasswordVerified | Accept a wrong full password. | Remove full-password validation; separately remove initial-token validation. |
| RSAKeyRequiresRequest | Send the RSA public key without its request byte. | Ignore an invalid RSA key request. |
| DrainWaitsForStatement | Close while the admitted statement runs. | Omit the statement wait in closeGracefully. |
| ReadAheadPreservesCommand | Lose the buffered next command. | Return nil from the real read-ahead watch. |
| IdleExpiryRollsBack | Keep the transaction after timeout. | Omit rollbackTransaction. |
| RevocationStopsDispatch | Dispatch after revocation. | Bypass the revoked-session guard. |
| CachedTypesAvailable | Lose types before the second execution. | Drop the cached parameter types. |
| ConnectionResetReleasesPrepared | Retain prepared ownership after reset. | Omit closeAllPrepared. |
| DisabledAccountCannotAuthenticate | Authenticate a locked account. | Ignore the locked flag in account lookup. |
| DisableRevokesSession | Retain the session when the account is disabled. | Store false in its revocation flag. |
| ShowRetainsWarnings | Clear warnings on SHOW. | Start every statement with clearDiagnostics. |
| OrdinaryClearsWarnings | Retain prior warnings on ordinary execution. | Omit the ordinary-statement clear. |
| ResetClearsWarnings | Retain warnings on connection reset. | Omit resetConnection diagnostics cleanup. |
| OnlySuccessfulPrefixApplied | Continue SET after the invalid assignment. | Continue after applySessionVariable returns an error. |
| SelectedDatabasePersists | Lose the selected database. | Omit the database assignment in databaseSelector.use. |

Authentication uses a real local net.Pipe exchange. It tests successful and wrong passwords over TLS and RSA, bad initial tokens on both paths, and a plaintext reply without TLS. The TLS certificate is local to the test. The RSA response uses the source nonce and real encryption. This checks protocol ordering and authentication guards. Cryptography, entropy, arbitrary transport failures, unsupported plugins, malformed packet variants, and authentication error cleanup remain outside the model.

The prepared probe allocates and executes `SELECT ?` twice through the real executor, first with a type and then with cached types. It checks long-data reset, connection reset, statement count, diagnostics reset, and database restoration. It covers one tiny-integer parameter path. Other encodings, ID wrap, arbitrary allocation failures, and all command packet variants remain outside the model.

Account probes use the real catalog and account operations. They check a changed password hash, authentication lookup after lock/unlock/drop, and revocation of an existing session. Reconnect means a replacement session with a fresh revocation flag. Unlocking does not restore the old session. The probe observes revocation while the old session is marked active; it does not run a network command to completion on that session.

The drain probe runs closeGracefully with one active statement, checks admission rejection after listener close, and checks that the connection stays open until endStatement. Its short observation window and final closure are a bounded schedule check. The command probe uses the real read-ahead goroutine and actual timer expiry with an open transaction. Its final revocation check starts with a preloaded command. Arbitrary concurrent schedules, every cancellation/error branch, and KILL authorization remain outside these models.

Warnings cover one warning, two retained SHOW results, one ordinary statement, one failure, and reset. SET covers one selected database and three assignments: valid, unknown, valid. The third must not run. One-shot transaction settings, all settings, last-manager account protection, and full SQL semantics remain outside these models.

Each current and broken config has a checked chart and report under `histories/`. Chart views retain every cross-label transition; same-label steps are omitted and counted in the report. Authentication labels expose invalid acceptance and invalid RSA request states in broken charts. The drain view gives premature closure its own state, so the broken state has a definite rule mark. No chart merges rule-breaking and rule-holding states into a mayBreak mark. Current AuthenticationHistory has 96 states and 96 steps; its chart has 11 labels and 14 arrows and omits 16 same-label steps. These merged chart labels do not replace TLC's full-state invariant checks.

Charts were generated with `/statechart` from scratch directories outside the repository. Only chart.mmd and report.md were retained. No production source was changed. Temporary overlays are removed by the runner. The official skill validator passed. No owned artifact exceeds 100 MB.

Model the Domain guided the explicit phase and owned-state records. Prove It Works guided the real TLS/RSA exchange, real catalog and executor calls, and paired behavioral source faults.
