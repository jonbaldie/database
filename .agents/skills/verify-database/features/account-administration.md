# Account administration

The admin account created by `database init` can create further accounts, give
and take namespace privileges, change passwords, and it cannot remove the last
account manager. Accounts and grants survive a restart, and a revoked account
loses access at once.

## Sub-features

- `account-login` authenticates an account over the MySQL wire with
  `caching_sha2_password`.
- `account-create` creates an account with `CREATE USER ... IDENTIFIED BY`.
- `account-grant` gives a namespace privilege such as `DATA_READ`.
- `account-revoke` takes the privilege back and blocks the next read.
- `account-alter` changes an account password with `ALTER USER`.
- `account-last-manager` refuses to remove the last `ACCOUNT_MANAGER`.
- `account-metadata` limits catalog metadata to the namespaces an account may
  see.
- `account-durable` keeps accounts and grants across a restart.

## How to get to it (user POV)

- Connect as `admin` with the password given to `database init`.
- Send `CREATE USER`, `ALTER USER`, `DROP USER`, `GRANT`, and `REVOKE`.
- Connect as the new account and try to read the granted data.

## Driving it with control.sh

Preconditions:

- One instance is live for this run and `control.sh doctor <run>` passes.
- Database `shop` holds table `orders` with at least one row.
- No account named `reader` exists yet.

- **Create an account and grant a read.** Run
  `control.sh sql <run> "CREATE USER 'reader' IDENTIFIED BY 'reader-password-1'" "GRANT DATA_READ ON shop.* TO 'reader'"`.
  Both lines report `"ok":true`.
- **Use the granted privilege.** Run
  `control.sh sql <run> --user reader --password reader-password-1 'USE shop' 'SELECT total FROM orders'`.
  The read succeeds and returns the stored rows.
- **Revoke and confirm the block.** Run
  `control.sh sql <run> "REVOKE DATA_READ ON shop.* FROM 'reader'"`, then repeat
  the reader's `SELECT`. It now fails; record the `error_code`.
- **Change a password.** Run
  `control.sh sql <run> "GRANT DATA_READ ON shop.* TO 'reader'" "ALTER USER 'reader' IDENTIFIED BY 'changed-reader-password'"`.
  The old password then fails to connect and
  `control.sh sql <run> --user reader --password changed-reader-password 'SELECT 1'`
  succeeds.
- **Protect the last account manager.** Run
  `control.sh sql <run> "REVOKE ACCOUNT_MANAGER ON *.* FROM 'admin'"`. It fails
  rather than leaving the instance unmanageable.
- **Confirm privilege-scoped metadata.** As `reader`, run
  `control.sh sql <run> --user reader --password changed-reader-password 'SHOW DATABASES'`.
  Only the namespaces this account may see are listed.
- **Confirm durability.** Run `control.sh restart <run>`, then
  `control.sh sql <run> --user reader --password changed-reader-password 'SELECT 1'`.
  The account and its new password still work after the restart.
- **Proof.** Save the granted read, the revoked failure with its `error_code`,
  the refused last-manager revoke, and the post-restart login to
  `/tmp/verify-database-evidence/<run>/account-administration/`.

## Gotchas

- The admin account name is `admin` and its password is the run's
  `PASSWORD_FILE`. `control.sh sql` uses that by default; a non-admin account
  needs both `--user` and `--password`.
- Account names and passwords in SQL need single quotes, so wrap the whole
  statement in double quotes in the shell.
- A revoke does not close an already-open session's connection. Prove the block
  with a fresh `control.sh sql` call, which opens a new session.
- Passwords used in these recipes are throwaway values for a disposable
  instance. Never reuse a real credential in a verification run, and never put
  one in an evidence artifact.
- Privilege names are product names such as `DATA_READ` and `ACCOUNT_MANAGER`,
  not MySQL's `SELECT`/`INSERT` grants. Check
  `docs/account-administration.md` before inventing one.
- Authentication uses `caching_sha2_password`. A client that cannot do it, or a
  plaintext-only client on a TLS-required setup, fails at connect time, which is
  a client problem and not a server bug.

