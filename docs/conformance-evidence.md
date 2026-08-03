# v0.1 conformance evidence map

This document maps every [Issue #1] story and each normative contract area to
public black-box evidence and release documentation. The principal system seam
is the built `database` executable. Black-box tests start that executable and
use only supported operator commands, the MySQL wire protocol and SQL, query
explanations, and diagnostics HTTP endpoints.

Supporting component tests may locate a defect, but they cannot establish a
public product claim alone. This map is the release audit required by story 69
and issue [#72](https://github.com/jonbaldie/database/issues/72).

[Issue #1]: https://github.com/jonbaldie/database/issues/1

## Evidence commands

`make quality` runs format checks, `go vet`, race-enabled tests, the built
executable, and the full production `messgo` gate.

Use these public checks for the main product surfaces:

```sh
go test ./test/blackbox -count=1
./scripts/validate-query-explanation-schema.sh
./scripts/verify-release.sh
```

The external driver check needs the installed, pinned real drivers that
`docs/compatibility-evidence.md` lists. It does not use driver mocks.

```sh
DATABASE_COMPATIBILITY_DRIVERS=1 \
go test ./test/blackbox -run '^TestExternalDriverCompatibilityProfile$' -count=1
```

`make performance` runs the fixed performance acceptance harness and writes
`dist/performance-evidence.json`. See the performance section below for the
release judgment rule.

## Seams under test

| Seam | Observable surface |
| --- | --- |
| Built `database` executable | Operator command family results, exit classes, progress, secret handling |
| MySQL classic protocol and SQL | Handshake, authentication, text and prepared execution, errors, SQLSTATE |
| Query explanation | Versioned JSON and table projections against the published schema |
| Diagnostics HTTP | `/live`, `/ready`, `/metrics` |
| Release scripts and docs | Native and OCI artifacts, digests, published baselines |

## Application developer story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 1–7: driver access, protocol rejection, authentication, prepared parity, metadata, error identity, and unsupported SQL rejection | [MySQL SQL behaviour](mysql-sql-behaviour.md), [driver profile](compatibility-evidence.md) | `TestGoDriverCompatibilityProfile`, `TestExternalDriverCompatibilityProfile`, `TestMySQLTLSAuthenticationTextLiteralAndProtocolFailures`, `TestMySQLCRUDStatementsAreAtomicAndPreparedExecutionMatchesText`, `TestMySQLPreparedStatementsUseBinaryRowsAndResetSafely`, `TestMySQLTextErrorsKeepWireConnectionReady` |
| 8–10: namespaces, DDL, and atomic schema change | [MySQL SQL behaviour](mysql-sql-behaviour.md), [catalog metadata](catalog-metadata.md) | `TestMySQLNamespacesAndBasicTablesSurviveRestartAndSupportQualifiedAccess`, `TestMySQLTableLifecycleSupportsRenameTruncateAndDrop`, `TestMySQLCatalogReturnsCanonicalCreateDefinitions`, `TestConstraintSurfaceThroughMySQL` |
| 11–13: relational constraints, B-tree indexes, and index hints | [MySQL SQL behaviour](mysql-sql-behaviour.md), [query explanation](query-explanation/README.md) | `TestConstraintSurfaceThroughMySQL`, `TestMySQLBTreeIndexesUseThePublicWireContract` |
| 14–15: strict values, collation, and SQL identifiers | [MySQL SQL behaviour](mysql-sql-behaviour.md), [domain vocabulary](../CONTEXT.md) | `TestMySQLStrictNumericAndBitSemantics`, `TestMySQLEnforcesCharacterCollationAndIdentifierSemantics` |
| 16: insert, replace, update, delete, upsert, and insert-select | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLReplaceUsesDeleteThenInsertAffectedRows`, `TestMySQLReplaceChecksForeignKeyDeletes`, `TestMySQLInsertOnDuplicateKeyUpdatesAtomically`, `TestMySQLInsertSelectSnapshotsTheSourceRows`, `TestMySQLInsertAndReplaceSetForms`, `TestMySQLExtendedMutationsRespectTransactionVisibility` |
| 17–20: relational shaping, functions, three-valued logic, and row order | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLRelationalShapingMatchesTextAndPreparedWirePaths`, `TestMySQLAggregatesAndWindowsUseThePublicWireContract`, `TestMySQLComposedQueriesMatchTextAndPreparedWirePaths`, `TestMySQLStrictNumericAndBitSemantics` |
| 21–27: isolation, atomicity, savepoints, locks, deadlocks, timeouts, cancellation, and read-only transactions | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLTransactionsProvideIsolationAndReadYourOwnWrites`, `TestMySQLTransactionsEnforceAutocommitReadOnlyAndAtomicErrors`, `TestSavepointsThroughMySQL`, `TestMySQLCoordinatesConcurrentLocks`, `TestMySQLLockModesTimeoutCancellationAndDeadlock` |
| 28–30: durable commit, interrupted commit, and recovery before service | [MySQL SQL behaviour](mysql-sql-behaviour.md), [operator automation](operator-automation.md) | `TestCrashRecoveryPreservesDurableCommitAndDropsInFlightTransaction`, `TestServingInstanceOwnsDirectoryRejectsDamageAndRollsBackOnStop` |
| 31–40: table and JSON query explanations, operators, source tracing, choice evidence, runtime evidence, safe analysis, live snapshots, and reproducibility | [query explanation](query-explanation/README.md) | `TestMySQLAnalyzeAndLiveExplanationUseTheWireContract`, `TestMySQLMutationFormsHavePublicPlans`, `TestMySQLBTreeIndexesUseThePublicWireContract`, `scripts/validate-query-explanation-schema.sh` |
| 41–43: result streaming, deadlines, cancellation, resource limits, spills, and diagnostics | [MySQL SQL behaviour](mysql-sql-behaviour.md), [server configuration](server-configuration.md) | `TestMySQLBoundsOrderedReadsAndPublishesResourceEvidence`, `TestMySQLLockModesTimeoutCancellationAndDeadlock`, `TestDiagnosticsHTTPContractIsObservableEndToEnd` |
| 44–45: closed session settings and connection reset | [session settings](session-settings.md) | `TestMySQLSessionSettingsUseTheWireContract`, `TestMySQLClientCanAuthenticatePersistAndResetSession`, `TestMySQLPreparedStatementsUseBinaryRowsAndResetSafely` |
| 46–47: canonical metadata and grant-filtered catalog visibility | [catalog metadata](catalog-metadata.md) | `TestMySQLCatalogReturnsCanonicalCreateDefinitions`, `TestMySQLMetadataIsHonestEscapedAndCommittedConsistent`, `TestMySQLCatalogMetadataFollowsNamespaceGrants` |
| 48: application session and query observation and control | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLSessionObservationAndQueryCancellation` |

## Account manager story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 49–51: account lifecycle, grants, and last account-manager protection | [account administration](account-administration.md) | `TestMySQLAccountAdministrationPersistsAcrossRestart`, `TestMySQLCatalogMetadataFollowsNamespaceGrants` |

## Operator story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 52–56: explicit initialization, command family, results, exit classes, diagnostics, and secret input | [operator automation](operator-automation.md) | `TestInitializeCreatesStoppedInspectableInstance`, `TestInitializeAcceptsStdinAndRejectsInlinePassword`, `TestInitializeRejectsAmbiguousOrMalformedSecretInputs`, `TestCommandFailureIsObservable`, `TestServeEmitsTerminalOperatorResult`, `TestOperatorShutdownStopsRunningServerWithResultAndProgress`, `TestOperatorShutdownRequiresYes`, `TestOperatorShutdownRejectsMissingOperationalControl` |
| 57–59: configuration source precedence, invalid input rejection, and bounded limits | [server configuration](server-configuration.md) | `TestOperatorConfigValidateAcceptsDefaultsAndRejectsUnknown`, `TestOperatorConfigValidateReportsFlagOverEnvironmentPrecedence`, `TestMySQLSessionCeilingRejectsAdditionalConnections`, `TestMySQLPreparedStatementCountIsBounded`, `TestMySQLBoundsOrderedReadsAndPublishesResourceEvidence` |
| 60–62: online backup, empty-target restore, and forward-only upgrade | [operator automation](operator-automation.md) | `TestOnlineBackupCreateCapturesCommittedStateWhileServerRuns`, `TestOnlineBackupCreateRejectsMissingOperationalControl`, `TestOnlineBackupCreateAcceptsPasswordStdinAddressResultJSON`, `TestOperatorBackupInspectAndRestoreRejectNonEmptyDestination`, `TestOperatorUpgradeUsesMatchingOnlineBackup` |
| 63–64: offline validation and corruption fail-closed behavior | [operator automation](operator-automation.md) | `TestOperatorDataValidateReportsHealthyStoppedInstance`, `TestOperatorDataValidateFailsClosedWithoutRepair`, `TestOperatorDataInspectDoesNotValidateOrRepair`, `TestServingInstanceOwnsDirectoryRejectsDamageAndRollsBackOnStop` |
| 65–66: liveness, readiness, metrics, and sensitivity-safe lifecycle events | [server configuration](server-configuration.md) | `TestDiagnosticsHTTPContractIsObservableEndToEnd`, `TestExecutableVersionAndLifecycleArePublic`, `TestServeEmitsTerminalOperatorResult` |

## Release and contributor story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 67: fixed native and OCI support | [distribution evidence](distribution.md) | `scripts/build-release.sh`, `scripts/verify-release.sh`, and the published tested examples in `distribution.md` |
| 68: fixed reference performance acceptance | [performance acceptance](performance-acceptance.md) | `make performance` / `scripts/performance.sh` write `dist/performance-evidence.json`. Maintainer judgment for issue #72 accepts the harness and published scenario as the release evidence path; a full Mac15,5 internal-SSD run is not required to close v0.1. |
| 69: every normative promise maps to public evidence | This document and [conformance guidance](conformance.md) | `TestConformanceEvidenceMapCoversEveryIssueStory` and this audit |
| 70: Apache-2.0 and maintainer-led contribution | [LICENSE](../LICENSE), [contribution rules](../CONTRIBUTING.md), and [governance](../GOVERNANCE.md) | Repository documents and pull-request review history |

## Normative contract inventory

| Contract area | Normative document | Public evidence focus |
| --- | --- | --- |
| SQL, protocol, transactions, durability, failure identities | [mysql-sql-behaviour.md](mysql-sql-behaviour.md) | Black-box MySQL wire tests listed above |
| Account administration | [account-administration.md](account-administration.md) | Account lifecycle and grant-filtered catalog tests |
| Catalog metadata surface | [catalog-metadata.md](catalog-metadata.md) | Canonical DDL, honest metadata, grant visibility |
| Query explanation | [query-explanation/README.md](query-explanation/README.md) | Wire EXPLAIN/ANALYZE/live tests and schema validation |
| Operator automation | [operator-automation.md](operator-automation.md) | Init, serve, shutdown, backup, restore, upgrade, validate, inspect |
| Server configuration registry | [server-configuration.md](server-configuration.md) | Config validate precedence/rejection and serve ceilings |
| Session settings registry | [session-settings.md](session-settings.md) | Session settings wire contract and connection reset |
| Driver compatibility | [compatibility-evidence.md](compatibility-evidence.md) | Go always-on profile; opt-in external drivers |
| Distribution | [distribution.md](distribution.md) | Release build and verify scripts |
| Performance acceptance | [performance-acceptance.md](performance-acceptance.md) | Harness, scenario versions, evidence JSON schema |
| Domain vocabulary and experimental bounds | [CONTEXT.md](../CONTEXT.md) | Docs use database account, database namespace, operator command family, and related glossary terms; v0.1 remains experimental and does not claim production readiness or complete MySQL compatibility |

## Explicit rejections covered by public evidence

| Rejection | Evidence |
| --- | --- |
| Inline password and ambiguous secret inputs | `TestInitializeAcceptsStdinAndRejectsInlinePassword`, `TestInitializeRejectsAmbiguousOrMalformedSecretInputs` |
| Unsupported or unknown configuration | `TestOperatorConfigValidateAcceptsDefaultsAndRejectsUnknown` |
| Shutdown without `--yes` | `TestOperatorShutdownRequiresYes` |
| Online backup or shutdown without operational control | `TestOnlineBackupCreateRejectsMissingOperationalControl`, `TestOperatorShutdownRejectsMissingOperationalControl` |
| Restore into a non-empty destination | `TestOperatorBackupInspectAndRestoreRejectNonEmptyDestination` |
| Upgrade without non-interactive confirmation | `TestOperatorUpgradeUsesMatchingOnlineBackup` |
| Corrupt durable catalog without silent repair | `TestOperatorDataValidateFailsClosedWithoutRepair`, `TestServingInstanceOwnsDirectoryRejectsDamageAndRollsBackOnStop` |
| Protocol and SQL failures that keep the session ready | `TestMySQLTLSAuthenticationTextLiteralAndProtocolFailures`, `TestMySQLTextErrorsKeepWireConnectionReady` |
| Read-only transactions reject mutations and locking reads | `TestMySQLTransactionsEnforceAutocommitReadOnlyAndAtomicErrors` |
| Session and prepared-statement ceilings | `TestMySQLSessionCeilingRejectsAdditionalConnections`, `TestMySQLPreparedStatementCountIsBounded` |
| Catalog visibility denied without namespace grants | `TestMySQLCatalogMetadataFollowsNamespaceGrants` |
| Last account-manager protection | `TestMySQLAccountAdministrationPersistsAcrossRestart` |
| Invalid values under strict type rules | `TestMySQLStrictNumericAndBitSemantics`, `TestMySQLEnforcesCharacterCollationAndIdentifierSemantics` |
| Lock timeout, cancellation, and deadlock identities | `TestMySQLLockModesTimeoutCancellationAndDeadlock` |

## Final release audit

Audit date: 2026-08-03.

Findings:

1. Every Issue #1 story from 1 through 70 has a mapped public evidence pointer in this document and in `conformance-evidence.json`. `TestConformanceEvidenceMapCoversEveryIssueStory` checks that inventory for completeness and that each pointer names an existing black-box test or repository path.
2. The previously recorded implementation gaps for online backup `--address` and `database shutdown` are closed on `main` and covered by black-box tests; this audit maps them.
3. Offline upgrade now accepts a matching online backup against a durable row-engine directory by comparing catalog schema without live `rows/` engine files. That matcher change is required so story 62 has public executable evidence.
4. Documentation uses the canonical domain vocabulary and states that v0.1 is experimental, with finite compatibility and support bounds in the linked contracts.
5. Performance story 68 follows the maintainer judgment recorded on issue #72: a full Mac15,5 internal-SSD acceptance run is not required for this release audit; the harness and scenario remain the normative evidence path. This does not widen the published reference environment in `CONTEXT.md`.
6. No unresolved conflict with recorded product decisions was found. Missing ADRs remain intentional: the final Issue #1 audit recorded none.

Release judgment: **no unmapped Issue #1 story or normative contract area remains, and no unresolved product-decision conflict remains for v0.1 conformance evidence.**
