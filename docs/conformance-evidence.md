# Draft v0.1 conformance evidence map

This document maps each [Issue #1] story to its contract and current evidence.
The principal system seam is the built `database` executable. The black-box
tests start that executable and use only supported operator commands, the MySQL
wire protocol and SQL, query explanations, and diagnostics HTTP endpoints.

The map is an audit work item, not a claim of release acceptance. A supporting
component test can help locate evidence, but it cannot establish a public
product claim alone. The performance acceptance gate is still open. See the
final status section.

[Issue #1]: https://github.com/jonbaldie/database/issues/1

## Evidence commands

`make quality` runs format checks, `go vet`, race-enabled tests, the built
executable, and the full production `messgo` gate. Pull requests separately
run the pinned changed-code Mutago gate.

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

## Application developer story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 1–7: driver access, protocol rejection, authentication, prepared parity, metadata, error identity, and unsupported SQL rejection | [MySQL SQL behaviour](mysql-sql-behaviour.md), [driver profile](compatibility-evidence.md) | `TestGoDriverCompatibilityProfile`, `TestExternalDriverCompatibilityProfile`, `TestMySQLTLSAuthenticationTextLiteralAndProtocolFailures`, `TestMySQLCRUDStatementsAreAtomicAndPreparedExecutionMatchesText`, `TestMySQLPreparedStatementsUseBinaryRowsAndResetSafely`, and `TestMySQLTextErrorsKeepWireConnectionReady` |
| 8–10: namespaces, DDL, and atomic schema change | [MySQL SQL behaviour](mysql-sql-behaviour.md), [catalog metadata](catalog-metadata.md) | `TestMySQLNamespacesAndBasicTablesSurviveRestartAndSupportQualifiedAccess`, `TestMySQLCatalogReturnsCanonicalCreateDefinitions`, and the executable DDL black-box checks |
| 11–13: relational constraints, B-tree indexes, and index hints | [MySQL SQL behaviour](mysql-sql-behaviour.md), [query explanation](query-explanation/README.md) | `TestConstraintSurfaceThroughMySQL` and `TestMySQLBTreeIndexesUseThePublicWireContract` |
| 14–15: strict values, collation, and SQL identifiers | [MySQL SQL behaviour](mysql-sql-behaviour.md), [domain vocabulary](../CONTEXT.md) | `TestMySQLStrictNumericAndBitSemantics` and `TestMySQLEnforcesCharacterCollationAndIdentifierSemantics` |
| 16: insert, replace, update, delete, upsert, and insert-select | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLReplaceUsesDeleteThenInsertAffectedRows`, `TestMySQLReplaceChecksForeignKeyDeletes`, `TestMySQLInsertOnDuplicateKeyUpdatesAtomically`, `TestMySQLInsertSelectSnapshotsTheSourceRows`, `TestMySQLInsertAndReplaceSetForms`, and `TestMySQLExtendedMutationsRespectTransactionVisibility` |
| 17–20: relational shaping, functions, three-valued logic, and row order | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLRelationalShapingMatchesTextAndPreparedWirePaths`, `TestMySQLAggregatesAndWindowsUseThePublicWireContract`, `TestMySQLComposedQueriesMatchTextAndPreparedWirePaths`, and `TestMySQLStrictNumericAndBitSemantics` |
| 21–27: isolation, atomicity, savepoints, locks, deadlocks, timeouts, cancellation, and read-only transactions | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLTransactionsProvideIsolationAndReadYourOwnWrites`, `TestMySQLTransactionsEnforceAutocommitReadOnlyAndAtomicErrors`, `TestSavepointsThroughMySQL`, `TestMySQLCoordinatesConcurrentLocks`, and `TestMySQLLockModesTimeoutCancellationAndDeadlock` |
| 28–30: durable commit, interrupted commit, and recovery before service | [MySQL SQL behaviour](mysql-sql-behaviour.md), [operator automation](operator-automation.md) | `TestCrashRecoveryPreservesDurableCommitAndDropsInFlightTransaction` is public black-box evidence. `TestServeRejectsIncompleteDurableRecoveryArtifacts` is supporting component evidence. |
| 31–40: table and JSON query explanations, operators, source tracing, choice evidence, runtime evidence, safe analysis, live snapshots, and reproducibility | [query explanation](query-explanation/README.md) | `TestMySQLAnalyzeAndLiveExplanationUseTheWireContract`, `TestMySQLMutationFormsHavePublicPlans`, `TestMySQLBTreeIndexesUseThePublicWireContract`, and `scripts/validate-query-explanation-schema.sh` |
| 41–43: result streaming, deadlines, cancellation, resource limits, spills, and diagnostics | [MySQL SQL behaviour](mysql-sql-behaviour.md), [server configuration](server-configuration.md) | `TestMySQLBoundsOrderedReadsAndPublishesResourceEvidence`, `TestMySQLLockModesTimeoutCancellationAndDeadlock`, and `TestDiagnosticsHTTPContractIsObservableEndToEnd` |
| 44–45: closed session settings and connection reset | [session settings](session-settings.md) | `TestMySQLSessionSettingsUseTheWireContract`, `TestMySQLClientCanAuthenticatePersistAndResetSession`, and `TestMySQLPreparedStatementsUseBinaryRowsAndResetSafely` |
| 46–47: canonical metadata and grant-filtered catalog visibility | [catalog metadata](catalog-metadata.md) | `TestMySQLCatalogReturnsCanonicalCreateDefinitions`, `TestMySQLMetadataIsHonestEscapedAndCommittedConsistent`, and `TestMySQLCatalogMetadataFollowsNamespaceGrants` |
| 48: application session and query observation and control | [MySQL SQL behaviour](mysql-sql-behaviour.md) | `TestMySQLSessionObservationAndQueryCancellation` |

## Account manager story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 49–51: account lifecycle, grants, and last account-manager protection | [account administration](account-administration.md) | `TestMySQLAccountAdministrationPersistsAcrossRestart` and `TestMySQLCatalogMetadataFollowsNamespaceGrants` |

## Operator story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 52–56: explicit initialization, command family, results, exit classes, diagnostics, and secret input | [operator automation](operator-automation.md) | Initialization and serve have black-box evidence: `TestInitializeCreatesStoppedInspectableInstance`, `TestInitializeAcceptsStdinAndRejectsInlinePassword`, `TestInitializeRejectsAmbiguousOrMalformedSecretInputs`, `TestCommandFailureIsObservable`, and `TestServeEmitsTerminalOperatorResult`. Command-output unit checks are supporting evidence. |
| 57–59: configuration source precedence, invalid input rejection, and bounded limits | [server configuration](server-configuration.md) | Session and prepared-statement limits have black-box evidence: `TestMySQLSessionCeilingRejectsAdditionalConnections` and `TestMySQLPreparedStatementCountIsBounded`. Configuration resolver tests are supporting evidence; public command coverage remains to be mapped. |
| 60–62: online backup, empty-target restore, and forward-only upgrade | [operator automation](operator-automation.md) | Current checks are supporting command-package evidence. Public executable black-box coverage remains to be mapped. |
| 63–64: offline validation and corruption fail-closed behavior | [operator automation](operator-automation.md) | Current data-command and lifecycle checks are supporting evidence. Public executable black-box coverage remains to be mapped. |
| 65–66: liveness, readiness, metrics, and sensitivity-safe lifecycle events | [server configuration](server-configuration.md) | `TestDiagnosticsHTTPContractIsObservableEndToEnd` is public black-box evidence. Lifecycle component checks are supporting evidence. |

## Release and contributor story map

| Issue #1 stories | Normative contract | Public evidence |
| --- | --- | --- |
| 67: fixed native and OCI support | [distribution evidence](distribution.md) | `scripts/build-release.sh`, `scripts/verify-release.sh`, and the published tested examples in `distribution.md` |
| 68: fixed reference performance acceptance | [performance acceptance](performance-acceptance.md) | `make performance` and `scripts/performance.sh`; release evidence is pending in Issue #71 |
| 69: every normative promise maps to public evidence | This document and [conformance guidance](conformance.md) | This draft maps every Issue #1 story. It still needs a line-level map for each normative contract rule and explicit rejection. |
| 70: Apache-2.0 and maintainer-led contribution | [LICENSE](../LICENSE), [contribution rules](../CONTRIBUTING.md), and [governance](../GOVERNANCE.md) | Repository documents and pull-request review history |

## Final release status

This draft maps every Issue #1 story but does not yet prove every normative
contract rule and explicit rejection. It records the open performance
dependency honestly: Issue #71 needs five valid runs for each of the four
operation gates and ten clean starts on the published reference environment.
Until the contract inventory and the performance evidence are accepted, v0.1
is not release-accepted and Issue #72 must remain open.
