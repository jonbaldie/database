# v0.1 account-administration SQL contract

This document is the normative v0.1 contract for database accounts and grants.
It is deliberately a finite MySQL-shaped surface, not a general MySQL account
administration feature. The [MySQL SQL behaviour contract](mysql-sql-behaviour.md)
governs MySQL 8.4.11 outcomes not explicitly changed here.

## Accounts and credentials

A **database account** has exactly one enabled-or-suspended, server-wide
**account name**. An account name has no host component and is compared
case-sensitively. It is 1 through 32 ASCII characters; its first character is
an ASCII letter or digit, and every later character is an ASCII letter, digit,
`.`, `_`, or `-`. A name supplied in the following syntax is a SQL string
literal whose decoded value must meet that rule.

```sql
CREATE USER [IF NOT EXISTS] 'name' IDENTIFIED BY 'password'
ALTER USER [IF EXISTS] 'name' IDENTIFIED BY 'password'
ALTER USER [IF EXISTS] CURRENT_USER IDENTIFIED BY 'password'
ALTER USER [IF EXISTS] 'name' ACCOUNT LOCK
ALTER USER [IF EXISTS] 'name' ACCOUNT UNLOCK
DROP USER [IF EXISTS] 'name'
```

`CREATE USER` creates one enabled account with no grants. It never creates a
host-qualified identity, and `GRANT` never creates an account implicitly.
Each statement has exactly one account target: MySQL multi-account forms are
unsupported. The parentheses in prose such as `('name' | CURRENT_USER)` denote
alternatives, not SQL punctuation.

The only authentication method is `caching_sha2_password`. A password is 12
through 1,024 UTF-8 bytes. Empty passwords; supplied authentication strings or
verifiers; plugin selection; password expiry, history, or composition rules;
and multiple credentials are unsupported. Credentials and verifiers are never
shown by SQL introspection, diagnostics, or operational evidence.

An account may change its own password, whether it identifies itself by its
literal name or `CURRENT_USER`. `ACCOUNT_MANAGER` also authorizes a password
change for a different account; no old-password proof is required for either
permitted form. No other self-service lifecycle action is implied: creating,
locking, unlocking, dropping, and changing grants require `ACCOUNT_MANAGER`.
Account renaming, host identities, anonymous accounts, proxy users, per-account
resource quotas, multifactor authentication, and certificate-bound accounts are
unsupported.

Changing a password affects only new authentication. Existing sessions remain
valid until their ordinary lifecycle ends. Authentication failures for a
missing account, suspended account, and incorrect password deliberately expose
the same failure class.

## Grants and authorization

The public grant vocabulary and scopes are closed:

| Scope | Privileges |
| --- | --- |
| `namespace.*` | `DATA_READ`, `DATA_WRITE`, `SCHEMA_MANAGEMENT` |
| `*.*` | `OPERATIONAL_OBSERVATION`, `OPERATIONAL_CONTROL`, `ACCOUNT_MANAGER`, `NAMESPACE_MANAGER` |

```sql
GRANT privilege ON scope TO 'name'
REVOKE privilege ON scope FROM 'name'
```

Each grant statement changes one named privilege for one existing account. A
namespace scope names an existing database namespace using the ordinary SQL
identifier rules; it is not a future-namespace wildcard. Table- and
column-level grants, roles, ownership, proxy users, and `WITH GRANT OPTION`
are unsupported. Granting an already-held grant is a successful no-op;
revoking a grant that is absent is an error.

No grant implies another. In particular, write does not imply read, schema
management does not imply data access, and account or namespace management does
not bypass data authorization. Every account may observe and cancel its own
work. `OPERATIONAL_OBSERVATION` and `OPERATIONAL_CONTROL` extend those powers
server-wide.

Only an enabled account holding `ACCOUNT_MANAGER` may create, lock, unlock, or
drop an account; change another account's password; or grant or revoke a
privilege. There is no per-grant delegation. Authorization is checked before a
requested account or grant effect occurs.

`NAMESPACE_MANAGER` authorizes `CREATE DATABASE` and `DROP DATABASE`; it does
not confer data or schema access. Creating a namespace atomically grants its
creator `DATA_READ`, `DATA_WRITE`, and `SCHEMA_MANAGEMENT` on that namespace.
Those are ordinary revocable grants, not ownership. Dropping a namespace
removes every grant scoped to it; recreating its name creates a new grant
boundary and never revives removed grants.

Server initialization creates one enabled initial administrator with
`ACCOUNT_MANAGER`, `NAMESPACE_MANAGER`, `OPERATIONAL_OBSERVATION`, and
`OPERATIONAL_CONTROL`, and no hidden superuser bypass. An operation is rejected
if its committed result would leave no enabled account holding
`ACCOUNT_MANAGER`. This protects against dropping or suspending the last
manager and revoking its grant.

## Lifecycle, transaction, and outcome rules

Locking prevents new authentication and, after each affected session finishes
its current statement, ends that session and rolls back any open transaction.
Unlocking preserves grants but does not revive ended sessions. Dropping also
prevents new authentication, removes the account and all of its grants, then
ends affected sessions after their current statements and rolls back open
transactions.

Permission changes take effect when an affected session's next statement
begins. A statement already in flight retains its starting authorization; a
permission change never silently rolls back the affected session's transaction.

Every account-administration or grant statement is single-target, atomic, and
durable with respect to its requested public effects. These statements use the
MySQL-style implicit-commit boundaries specified by the SQL behaviour baseline,
so a later user `ROLLBACK` never undoes their completed account or grant
effects. A rejected statement has none of its requested public effects; its
implicit-commit and diagnostic behaviour otherwise follows that baseline.

`IF NOT EXISTS` on duplicate creation and `IF EXISTS` on a missing alter,
lock, unlock, or drop are successful no-ops with a retrievable warning. Without
the modifier, the corresponding duplicate or missing lifecycle operation is an
error. Unknown accounts, namespaces, or privileges; malformed account names or
credentials; unsupported clauses; authorization failures; and the last-manager
invariant all fail explicitly with no partial requested effect. The session
remains usable.

Where MySQL 8.4.11 has an equivalent error or warning, its numeric identity and
SQLSTATE are stable compatibility data. For an intentional v0.1 deviation, the
closest applicable MySQL identity is used. Message wording is not stable.

## Introspection and evidence

```sql
SHOW GRANTS
SHOW GRANTS FOR 'name'
SHOW GRANTS FOR CURRENT_USER
```

`SHOW GRANTS` reports the current account. The `FOR` forms may report a
different account only to an `ACCOUNT_MANAGER`; `CURRENT_USER` resolves to the
authenticated account. `information_schema.ACCOUNTS` is read-only and exposes
only account name and suspension state. `information_schema.ACCOUNT_GRANTS` is
read-only and exposes only account name, privilege, scope kind, and namespace
where applicable.

An ordinary account sees only its own account and grant rows. An
`ACCOUNT_MANAGER` sees all accounts and grants. Neither surface exposes
credential material. Every successful or rejected account, credential, or
grant change emits structured operational evidence containing actor, target,
operation, and outcome, with no secret material. v0.1 does not promise a
durable security-audit ledger.
