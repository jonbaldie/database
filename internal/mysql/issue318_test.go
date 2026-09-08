package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIssue318CrossDatabaseGrantAuthorization(t *testing.T) {
	executor := ddlExecutorForTest(t)
	store := executor.server.config.Catalog

	if err := store.CreateNamespace("db_public"); err != nil {
		t.Fatalf("create db_public: %v", err)
	}
	if err := store.CreateNamespace("db_secret"); err != nil {
		t.Fatalf("create db_secret: %v", err)
	}

	for _, stmt := range []string{
		"CREATE TABLE db_secret.confidential (secret_data VARCHAR(100) PRIMARY KEY)",
		"INSERT INTO db_secret.confidential VALUES ('nuclear_launch_codes')",
	} {
		if _, err := executeStatement(executor, stmt); err != nil {
			t.Fatalf("admin setup %q: %v", stmt, err)
		}
	}

	if err := store.CreateAccount(catalog.Account{
		Name:         "bob",
		PasswordHash: "secret",
		Grants: []catalog.Grant{
			{Privilege: "DATA_READ", Namespace: "db_public"},
			{Privilege: "DATA_WRITE", Namespace: "db_public"},
			{Privilege: "SCHEMA_MANAGEMENT", Namespace: "db_public"},
		},
	}); err != nil {
		t.Fatalf("create user bob: %v", err)
	}

	t.Run("select with empty session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "",
				initialDB:       "",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "SELECT * FROM db_secret.confidential")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("insert with db_public session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "INSERT INTO db_secret.confidential VALUES ('unauthorized_row')")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("create table with db_public session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "CREATE TABLE db_secret.pwned (id INT)")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("truncate table with db_public session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "TRUNCATE TABLE db_secret.confidential")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("update with db_public session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "UPDATE db_secret.confidential SET secret_data = 'hacked'")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("delete with db_public session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "DELETE FROM db_secret.confidential")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("alter table with db_public session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "ALTER TABLE db_secret.confidential ADD COLUMN extra INT")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("drop table with db_public session database should require grant on target db", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "DROP TABLE db_secret.confidential")
		if err == nil {
			t.Fatalf("expected access denied error, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("cross-database join should require grant on all target tables", func(t *testing.T) {
		if _, err := executeStatement(executor, "CREATE TABLE db_public.allowed (id INT PRIMARY KEY)"); err != nil {
			t.Fatalf("create db_public.allowed: %v", err)
		}
		if _, err := executeStatement(executor, "INSERT INTO db_public.allowed VALUES (1)"); err != nil {
			t.Fatalf("insert db_public.allowed: %v", err)
		}

		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "db_public",
				initialDB:       "db_public",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "SELECT * FROM db_public.allowed JOIN db_secret.confidential ON 1=1")
		if err == nil {
			t.Fatalf("expected access denied error on cross-database join, got nil")
		}
		if !isFailureCode(err, 1044) && !isFailureCode(err, 1142) {
			t.Fatalf("expected error 1044 or 1142, got %v", err)
		}
	})

	t.Run("unqualified table without session database should return 1046", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "",
				initialDB:       "",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		_, err := executeStatement(bobExecutor, "SELECT * FROM confidential")
		if err == nil || !isFailureCode(err, 1046) {
			t.Fatalf("expected 1046 no database selected, got %v", err)
		}
	})

	t.Run("select 1 without session database should succeed", func(t *testing.T) {
		bobExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "bob",
				database:        "",
				initialDB:       "",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		res, err := executeStatement(bobExecutor, "SELECT 1")
		if err != nil {
			t.Fatalf("expected select 1 to succeed, got %v", err)
		}
		if len(res.rows) != 1 || res.rows[0][0] != "1" {
			t.Fatalf("unexpected select 1 result: %#v", res)
		}
	})

	t.Run("user with legitimate grant on db_secret should succeed", func(t *testing.T) {
		if err := store.CreateAccount(catalog.Account{
			Name:         "alice",
			PasswordHash: "secret",
			Grants: []catalog.Grant{
				{Privilege: "DATA_READ", Namespace: "db_secret"},
				{Privilege: "DATA_WRITE", Namespace: "db_secret"},
				{Privilege: "SCHEMA_MANAGEMENT", Namespace: "db_secret"},
			},
		}); err != nil {
			t.Fatalf("create user alice: %v", err)
		}

		aliceExecutor := &textStatementExecutor{
			session: &session{
				server:          executor.server,
				username:        "alice",
				database:        "",
				initialDB:       "",
				timeZone:        "UTC",
				initialTimeZone: "UTC",
				statements:      map[uint32]*preparedStatement{},
			},
		}

		res, err := executeStatement(aliceExecutor, "SELECT * FROM db_secret.confidential")
		if err != nil {
			t.Fatalf("alice qualified select: %v", err)
		}
		if len(res.rows) != 1 || res.rows[0][0] != "nuclear_launch_codes" {
			t.Fatalf("alice select rows = %#v", res.rows)
		}

		if _, err := executeStatement(aliceExecutor, "INSERT INTO db_secret.confidential VALUES ('authorized_row')"); err != nil {
			t.Fatalf("alice qualified insert: %v", err)
		}

		if _, err := executeStatement(aliceExecutor, "UPDATE db_secret.confidential SET secret_data = 'updated_code' WHERE secret_data = 'authorized_row'"); err != nil {
			t.Fatalf("alice qualified update: %v", err)
		}

		if _, err := executeStatement(aliceExecutor, "DELETE FROM db_secret.confidential WHERE secret_data = 'updated_code'"); err != nil {
			t.Fatalf("alice qualified delete: %v", err)
		}

		if _, err := executeStatement(aliceExecutor, "CREATE TABLE db_secret.alice_table (id INT)"); err != nil {
			t.Fatalf("alice qualified create table: %v", err)
		}

		if _, err := executeStatement(aliceExecutor, "TRUNCATE TABLE db_secret.alice_table"); err != nil {
			t.Fatalf("alice qualified truncate table: %v", err)
		}
	})
}
