# Account administration

Run from the repository root with the evidence helper in the parent skill.

```sh
verify_command accounts env -u DATABASE_BLACKBOX_EXECUTABLE GOMAXPROCS=2 GOFLAGS='-p=2' \
  go test ./test/blackbox \
  -run '^TestMySQL(AccountAdministrationPersistsAcrossRestart|LastAccountManagerProtection)$' -count=1 -v
```

These tests use private server processes and MySQL wire clients. They check
account and grant changes after restart, rejection of lock, drop, and revoke
for the last enabled account manager, and repeated lock of an account that is
already locked. A rejected statement must leave the session usable.

`TestConcurrentAccountWritesKeepOneManager` in `internal/catalog` checks
concurrent lock, drop, and revoke through the catalog account-write interface.
It does not prove safety against concurrent DML; that requires issue #568.
