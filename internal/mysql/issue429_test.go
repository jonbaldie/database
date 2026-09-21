package mysql

import (
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestIssue429HiddenNamespaceUsesOneVisibilityRule(t *testing.T) {
	executor := ddlExecutorForTest(t)
	store := executor.server.config.Catalog
	if err := store.CreateNamespace("hidden"); err != nil {
		t.Fatalf("create hidden namespace: %v", err)
	}
	if _, err := executeStatement(executor, "CREATE TABLE hidden.items (id INT)"); err != nil {
		t.Fatalf("create hidden table: %v", err)
	}
	if err := store.CreateAccount(catalog.Account{Name: "reader", PasswordHash: "hash"}); err != nil {
		t.Fatalf("create reader: %v", err)
	}

	reader := &textStatementExecutor{session: &session{
		server: executor.server, username: "reader", timeZone: "UTC", initialTimeZone: "UTC",
		statements: map[uint32]*preparedStatement{},
	}}

	for _, test := range []struct {
		name  string
		query string
		code  uint16
	}{
		{name: "use hidden", query: "USE hidden", code: 1044},
		{name: "show tables hidden", query: "SHOW TABLES FROM hidden", code: 1044},
		{name: "show create hidden", query: "SHOW CREATE DATABASE hidden", code: 1044},
		{name: "show columns hidden", query: "SHOW COLUMNS FROM items FROM hidden", code: 1044},
		{name: "use missing", query: "USE missing", code: 1049},
		{name: "show tables missing", query: "SHOW TABLES FROM missing", code: 1049},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := executeStatement(reader, test.query); !isFailureCode(err, test.code) {
				t.Fatalf("%s error = %v, want %d", test.query, err, test.code)
			}
		})
	}

	if _, err := executeStatement(reader, "USE information_schema"); err != nil {
		t.Fatalf("USE information_schema: %v", err)
	}
}

func TestIssue429NamespaceManagerSeesNamesOnly(t *testing.T) {
	executor := ddlExecutorForTest(t)
	store := executor.server.config.Catalog
	if err := store.CreateNamespace("hidden"); err != nil {
		t.Fatalf("create hidden namespace: %v", err)
	}
	if _, err := executeStatement(executor, "CREATE TABLE hidden.items (id INT)"); err != nil {
		t.Fatalf("create hidden table: %v", err)
	}
	if err := store.CreateAccount(catalog.Account{
		Name:         "manager",
		PasswordHash: "hash",
		Grants:       []catalog.Grant{{Privilege: "NAMESPACE_MANAGER"}},
	}); err != nil {
		t.Fatalf("create manager: %v", err)
	}
	manager := &textStatementExecutor{session: &session{
		server: executor.server, username: "manager", timeZone: "UTC", initialTimeZone: "UTC",
		statements: map[uint32]*preparedStatement{},
	}}

	databases, err := executeStatement(manager, "SHOW DATABASES")
	if err != nil {
		t.Fatalf("SHOW DATABASES: %v", err)
	}
	if !equalRows(databases.rows, [][]string{{"app"}, {"hidden"}, {"information_schema"}}) {
		t.Fatalf("SHOW DATABASES rows = %#v", databases.rows)
	}
	schemata, err := executeStatement(manager, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA")
	if err != nil {
		t.Fatalf("information_schema.SCHEMATA: %v", err)
	}
	if !equalRows(schemata.rows, [][]string{{"information_schema"}, {"app"}, {"hidden"}}) {
		t.Fatalf("information_schema.SCHEMATA rows = %#v", schemata.rows)
	}
	tables, err := executeStatement(manager, "SELECT TABLE_SCHEMA, TABLE_NAME FROM information_schema.TABLES")
	if err != nil {
		t.Fatalf("information_schema.TABLES: %v", err)
	}
	for _, row := range tables.rows {
		if len(row) >= 2 && row[0] == "hidden" && row[1] == "items" {
			t.Fatalf("manager saw hidden table through information_schema.TABLES: %#v", tables.rows)
		}
	}
	for _, query := range []string{
		"USE hidden",
		"SHOW TABLES FROM hidden",
		"SHOW CREATE DATABASE hidden",
		"SHOW COLUMNS FROM items FROM hidden",
	} {
		if _, err := executeStatement(manager, query); !isFailureCode(err, 1044) {
			t.Fatalf("%s error = %v, want 1044", query, err)
		}
	}
}

func TestIssue429GrantAndDropResolveNamespaceVisibility(t *testing.T) {
	executor := ddlExecutorForTest(t)
	store := executor.server.config.Catalog
	if err := store.CreateNamespace("hidden"); err != nil {
		t.Fatalf("create hidden namespace: %v", err)
	}
	if err := store.CreateNamespace("to_drop"); err != nil {
		t.Fatalf("create to_drop namespace: %v", err)
	}
	if err := store.CreateAccount(catalog.Account{Name: "reader", PasswordHash: "hash"}); err != nil {
		t.Fatalf("create reader: %v", err)
	}
	if err := store.CreateAccount(catalog.Account{
		Name:         "manager",
		PasswordHash: "hash",
		Grants: []catalog.Grant{
			{Privilege: "ACCOUNT_MANAGER"},
			{Privilege: "NAMESPACE_MANAGER"},
		},
	}); err != nil {
		t.Fatalf("create manager: %v", err)
	}
	manager := &textStatementExecutor{session: &session{
		server: executor.server, username: "manager", timeZone: "UTC", initialTimeZone: "UTC",
		statements: map[uint32]*preparedStatement{},
	}}
	if _, err := executeStatement(manager, "GRANT DATA_READ ON hidden.* TO 'reader'"); err != nil {
		t.Fatalf("grant hidden: %v", err)
	}
	if _, err := executeStatement(manager, "GRANT DATA_READ ON missing.* TO 'reader'"); !isFailureCode(err, 1049) {
		t.Fatalf("grant missing error = %v, want 1049", err)
	}
	if _, err := executeStatement(manager, "DROP DATABASE to_drop"); err != nil {
		t.Fatalf("drop to_drop: %v", err)
	}
	if _, err := executeStatement(manager, "DROP DATABASE missing"); !isFailureCode(err, 1049) {
		t.Fatalf("drop missing error = %v, want 1049", err)
	}
	if _, err := executeStatement(manager, "DROP DATABASE IF EXISTS missing"); err != nil {
		t.Fatalf("drop missing if exists: %v", err)
	}

	reader := &textStatementExecutor{session: &session{
		server: executor.server, username: "reader", timeZone: "UTC", initialTimeZone: "UTC",
		statements: map[uint32]*preparedStatement{},
	}}
	if _, err := executeStatement(reader, "USE hidden"); err != nil {
		t.Fatalf("reader USE hidden after grant: %v", err)
	}
}

func TestIssue429AuthenticatorUsesAccountVisibility(t *testing.T) {
	executor := ddlExecutorForTest(t)
	store := executor.server.config.Catalog
	if err := store.CreateNamespace("hidden"); err != nil {
		t.Fatalf("create hidden namespace: %v", err)
	}
	if err := store.CreateAccount(catalog.Account{Name: "reader", PasswordHash: "hash"}); err != nil {
		t.Fatalf("create reader: %v", err)
	}
	authenticator := authenticator{config: Config{Catalog: store}}
	if err := authenticator.databaseExists("reader", "hidden"); !isFailureCode(err, 1044) {
		t.Fatalf("hidden default database error = %v, want 1044", err)
	}
	if err := authenticator.databaseExists("reader", "missing"); !isFailureCode(err, 1049) {
		t.Fatalf("missing default database error = %v, want 1049", err)
	}
	if err := authenticator.databaseExists("reader", informationSchemaName); err != nil {
		t.Fatalf("information_schema default database: %v", err)
	}
}
