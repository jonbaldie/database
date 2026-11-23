# Account initialization and administration

## User workflow

Run `database init`, start `database serve`, and use an administrator over the MySQL wire protocol. Account changes must survive a server restart. Existing data directories must keep their accounts during migration.

## Process tests

Run from the repository root in the shell described in the verification skill. Keep the command output and exit status.

```sh
verify_command accounts env -u DATABASE_BLACKBOX_EXECUTABLE GOMAXPROCS=2 GOFLAGS='-p=2' go test ./test/blackbox \
  -run '^(TestInitStoresAdministratorOnlyInCatalog|TestDroppedInitialAdministratorStaysDropped|TestInitialAdministratorPasswordChangeSurvivesRestart|TestLegacyBootstrapRunsOnceAndPreservesAccounts|TestMySQLAccountAdministrationPersistsAcrossRestart)$' \
  -count=1 -v
```

The tests inspect the catalog immediately after initialization and use the MySQL wire protocol before and after restart. The legacy cases cover an empty catalog and one with an existing account.
