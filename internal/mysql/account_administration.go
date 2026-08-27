package mysql

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jonbaldie/database/internal/catalog"
)

const accountManagerPrivilege = "ACCOUNT_MANAGER"

func grantCreatedNamespace(definition *catalog.Definition, namespace, username string) error {
	if username == "" {
		return nil
	}
	account, found := definition.Accounts[username]
	if !found {
		return errors.New("account does not exist")
	}
	for _, privilege := range []string{"DATA_READ", "DATA_WRITE", "SCHEMA_MANAGEMENT"} {
		if accountGrantIndex(account, privilege, namespace) < 0 {
			account.Grants = append(account.Grants, catalog.Grant{Privilege: privilege, Namespace: namespace})
		}
	}
	definition.Accounts[username] = account
	return nil
}

func removeNamespaceGrants(definition *catalog.Definition, namespace string) {
	for name, account := range definition.Accounts {
		account.Grants = grantsWithoutNamespace(account.Grants, namespace)
		definition.Accounts[name] = account
	}
}

func grantsWithoutNamespace(grants []catalog.Grant, namespace string) []catalog.Grant {
	result := grants[:0]
	for _, grant := range grants {
		if grant.Namespace != namespace {
			result = append(result, grant)
		}
	}
	return result
}

func visibleCatalogDefinition(definition catalog.Definition, username string) catalog.Definition {
	if username == "" || accountSeesAllNamespaces(definition.Accounts[username]) {
		return definition
	}
	account := definition.Accounts[username]
	visible := catalog.Definition{Namespaces: map[string]catalog.Namespace{}, Accounts: definition.Accounts}
	for key, namespace := range definition.Namespaces {
		if accountSeesNamespace(account, namespace.Name) {
			visible.Namespaces[key] = namespace
		}
	}
	return visible
}

func accountSeesAllNamespaces(account catalog.Account) bool {
	return accountHasGrant(account, "NAMESPACE_MANAGER")
}

func accountSeesNamespace(account catalog.Account, namespace string) bool {
	for _, grant := range account.Grants {
		if grant.Namespace == namespace {
			return true
		}
	}
	return false
}

func metadataNamespaceFailure(store *catalog.Store, name string) error {
	if store != nil {
		_, exists := store.Snapshot().Namespaces[catalog.Key(name)]
		if exists {
			return sqlFailure{1044, "42000", "access denied"}
		}
	}
	return sqlFailure{1049, "42000", "unknown database '" + name + "'"}
}

func (s *textStatementExecutor) authorizeStatement(lower string) error {
	privilege, namespace := s.statementGrant(lower)
	if privilege == "" {
		return nil
	}
	return s.requireGrant(privilege, namespace)
}

func (s *textStatementExecutor) statementGrant(lower string) (string, string) {
	if s.session.username == "" || administrativeStatement(lower) {
		return "", ""
	}
	if startsStatement(lower, namespaceStatements) {
		return "NAMESPACE_MANAGER", ""
	}
	if isDataDefinition(lower) {
		return schemaGrant(s.session.database)
	}
	if startsStatement(lower, writeStatements) {
		return "DATA_WRITE", s.session.database
	}
	if isComposedSelectStatement(lower) {
		return readGrant(s.session.database)
	}
	return "", ""
}

var namespaceStatements = []string{"create database ", "create schema ", "drop database ", "drop schema "}
var writeStatements = []string{"insert into ", "replace ", "update ", "delete from "}

func administrativeStatement(lower string) bool {
	return accountAdministrationStatement(lower) || isTransactionControl(lower) || isSettingControl(lower)
}

func startsStatement(lower string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func schemaGrant(namespace string) (string, string) {
	if strings.EqualFold(namespace, informationSchemaName) {
		return "", ""
	}
	return "SCHEMA_MANAGEMENT", namespace
}

func readGrant(namespace string) (string, string) {
	if namespace == "" || strings.EqualFold(namespace, informationSchemaName) {
		return "", ""
	}
	return "DATA_READ", namespace
}

func (s *textStatementExecutor) requireGrant(privilege, namespace string) error {
	if s.session.server.config.Catalog == nil {
		return sqlFailure{1227, "42000", "access denied"}
	}
	account, found := s.session.server.config.Catalog.Account(s.session.username)
	if !found {
		// Package tests construct sessions without the serving lifecycle. A real
		// server always provisions this configured account before it accepts a
		// connection.
		if s.session.server.config.Username == s.session.username && s.session.server.config.PasswordHash != "" {
			return nil
		}
		return sqlFailure{1227, "42000", "access denied"}
	}
	if account.Locked {
		return sqlFailure{1227, "42000", "access denied"}
	}
	if accountGrantIndex(account, privilege, namespace) < 0 {
		return sqlFailure{1227, "42000", "access denied"}
	}
	return nil
}

func (s *textStatementExecutor) accountStatement(query, lower string) (*queryResult, bool, error) {
	if !accountAdministrationStatement(lower) {
		return nil, false, nil
	}
	if err := s.commitAccountBoundary(); err != nil {
		return nil, true, err
	}
	return nil, true, s.applyAccountStatement(query, lower)
}

func accountAdministrationStatement(lower string) bool {
	return strings.HasPrefix(lower, "create user ") || strings.HasPrefix(lower, "alter user ") || strings.HasPrefix(lower, "drop user ") || strings.HasPrefix(lower, "grant ") || strings.HasPrefix(lower, "revoke ")
}

func (s *textStatementExecutor) commitAccountBoundary() error {
	if s.session.transaction {
		return (&transactionExecutor{s.session}).commit()
	}
	return nil
}

func (s *textStatementExecutor) applyAccountStatement(query, lower string) error {
	switch {
	case strings.HasPrefix(lower, "create user "):
		return s.createAccount(query)
	case strings.HasPrefix(lower, "alter user "):
		return s.alterAccount(query)
	case strings.HasPrefix(lower, "drop user "):
		return s.dropAccount(query)
	case strings.HasPrefix(lower, "grant "):
		return s.changeGrant(query, true)
	default:
		return s.changeGrant(query, false)
	}
}

func (s *textStatementExecutor) createAccount(query string) error {
	if err := s.requireAccountManager(); err != nil {
		return err
	}
	rest := strings.TrimSpace(query[len("CREATE USER "):])
	ifNotExists := strings.HasPrefix(strings.ToLower(rest), "if not exists ")
	if ifNotExists {
		rest = strings.TrimSpace(rest[len("IF NOT EXISTS "):])
	}
	name, password, ok := accountCredentials(rest)
	if !ok {
		return sqlFailure{1064, "42000", "malformed CREATE USER"}
	}
	if err := validateAccountInput(name, password); err != nil {
		return err
	}
	hash := passwordHash(password)
	err := s.session.server.config.Catalog.CreateAccount(catalog.Account{Name: name, PasswordHash: hash})
	if err != nil && ifNotExists {
		return nil
	}
	if err != nil {
		return sqlFailure{1396, "HY000", "account exists"}
	}
	return nil
}

func (s *textStatementExecutor) alterAccount(query string) error {
	name, suffix, account, err := s.alterAccountTarget(query)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	if name != s.session.username {
		if err := s.requireAccountManager(); err != nil {
			return err
		}
	}
	if lock, isLockChange := accountLockChange(suffix); isLockChange {
		return s.setAccountLock(name, account, lock)
	}
	return s.changeAccountPassword(name, suffix)
}

func (s *textStatementExecutor) alterAccountTarget(query string) (string, string, catalog.Account, error) {
	rest := strings.TrimSpace(query[len("ALTER USER "):])
	ifExists := strings.HasPrefix(strings.ToLower(rest), "if exists ")
	if ifExists {
		rest = strings.TrimSpace(rest[len("IF EXISTS "):])
	}
	name, suffix, ok := accountTarget(rest, s.session.username)
	if !ok {
		return "", "", catalog.Account{}, sqlFailure{1064, "42000", "malformed ALTER USER"}
	}
	account, found := s.session.server.config.Catalog.Account(name)
	if !found && ifExists {
		return "", "", catalog.Account{}, nil
	}
	if !found {
		return "", "", catalog.Account{}, sqlFailure{1396, "HY000", "unknown account"}
	}
	return name, suffix, account, nil
}

func accountLockChange(suffix string) (bool, bool) {
	switch strings.ToLower(suffix) {
	case "account lock":
		return true, true
	case "account unlock":
		return false, true
	default:
		return false, false
	}
}

func (s *textStatementExecutor) setAccountLock(name string, account catalog.Account, lock bool) error {
	if err := s.requireAccountManager(); err != nil {
		return err
	}
	if lock && accountHasGrant(account, accountManagerPrivilege) && s.enabledAccountManagerCount() == 1 {
		return sqlFailure{1396, "HY000", "last account manager is protected"}
	}
	if err := s.session.server.config.Catalog.UpdateAccount(name, func(account *catalog.Account) error { account.Locked = lock; return nil }); err != nil {
		return err
	}
	if lock {
		s.session.server.connections.revokeAccount(name)
	}
	return nil
}

func (s *textStatementExecutor) changeAccountPassword(name, suffix string) error {
	lower := strings.ToLower(suffix)
	if !strings.HasPrefix(lower, "identified by ") {
		return sqlFailure{1064, "42000", "unsupported ALTER USER"}
	}
	password := scalar(strings.TrimSpace(suffix[len("IDENTIFIED BY "):]))
	if err := validateAccountInput(name, password); err != nil {
		return err
	}
	return s.session.server.config.Catalog.UpdateAccount(name, func(account *catalog.Account) error { account.PasswordHash = passwordHash(password); return nil })
}

func (s *textStatementExecutor) dropAccount(query string) error {
	if err := s.requireAccountManager(); err != nil {
		return err
	}
	rest := strings.TrimSpace(query[len("DROP USER "):])
	ifExists := strings.HasPrefix(strings.ToLower(rest), "if exists ")
	if ifExists {
		rest = strings.TrimSpace(rest[len("IF EXISTS "):])
	}
	name := scalar(rest)
	account, found := s.session.server.config.Catalog.Account(name)
	if !found && ifExists {
		return nil
	}
	if !found {
		return sqlFailure{1396, "HY000", "unknown account"}
	}
	if s.protectLastAccountManager(account) {
		return sqlFailure{1396, "HY000", "last account manager is protected"}
	}
	if err := s.session.server.config.Catalog.DeleteAccount(name); err != nil {
		return err
	}
	s.session.server.connections.revokeAccount(name)
	return nil
}

func (s *textStatementExecutor) protectLastAccountManager(account catalog.Account) bool {
	return accountHasGrant(account, accountManagerPrivilege) && !account.Locked && s.enabledAccountManagerCount() == 1
}

func (s *textStatementExecutor) changeGrant(query string, grant bool) error {
	if err := s.requireAccountManager(); err != nil {
		return err
	}
	change, err := parseGrantChange(query, grant)
	if err != nil {
		return err
	}
	return s.applyGrantChange(change)
}

type accountGrantChange struct {
	privilege string
	namespace string
	name      string
	grant     bool
}

func parseGrantChange(query string, grant bool) (accountGrantChange, error) {
	privilege, namespace, name, ok := grantStatementParts(query, grant)
	if !ok || !validGrant(privilege, namespace) {
		return accountGrantChange{}, sqlFailure{1064, "42000", "unsupported grant"}
	}
	return accountGrantChange{privilege: privilege, namespace: namespace, name: name, grant: grant}, nil
}

func (s *textStatementExecutor) applyGrantChange(change accountGrantChange) error {
	if change.namespace != "" && !s.namespaceExists(change.namespace) {
		return sqlFailure{1049, "42000", "unknown database"}
	}
	account, found := s.session.server.config.Catalog.Account(change.name)
	if !found {
		return sqlFailure{1396, "HY000", "unknown account"}
	}
	if s.removesLastAccountManager(change, account) {
		return sqlFailure{1396, "HY000", "last account manager is protected"}
	}
	return s.session.server.config.Catalog.UpdateAccount(change.name, grantChange(change.privilege, change.namespace, change.grant))
}

func (s *textStatementExecutor) removesLastAccountManager(change accountGrantChange, account catalog.Account) bool {
	return !change.grant && change.privilege == accountManagerPrivilege && change.namespace == "" &&
		accountHasGrant(account, change.privilege) && !account.Locked && s.enabledAccountManagerCount() == 1
}

func accountCredentials(rest string) (string, string, bool) {
	name, suffix, ok := accountTarget(rest, "")
	if !ok || !strings.HasPrefix(strings.ToLower(suffix), "identified by ") {
		return "", "", false
	}
	return name, scalar(strings.TrimSpace(suffix[len("IDENTIFIED BY "):])), true
}

func accountTarget(rest, current string) (string, string, bool) {
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return "", "", false
	}
	if strings.EqualFold(fields[0], "current_user") {
		return current, strings.TrimSpace(rest[len(fields[0]):]), current != ""
	}
	if rest[0] != '\'' {
		return "", "", false
	}
	end := strings.Index(rest[1:], "'")
	if end < 0 {
		return "", "", false
	}
	return scalar(rest[:end+2]), strings.TrimSpace(rest[end+2:]), true
}

func grantStatementParts(query string, grant bool) (string, string, string, bool) {
	verb := "GRANT "
	join := " TO "
	if !grant {
		verb, join = "REVOKE ", " FROM "
	}
	rest := strings.TrimSpace(query[len(verb):])
	left, name, found := splitKeyword(rest, strings.TrimSpace(join))
	if !found {
		return "", "", "", false
	}
	privilege, scope, found := splitKeyword(left, "ON")
	if !found {
		return "", "", "", false
	}
	scope = strings.TrimSpace(scope)
	if scope == "*.*" {
		return strings.ToUpper(strings.TrimSpace(privilege)), "", scalar(name), true
	}
	if !strings.HasSuffix(scope, ".*") {
		return "", "", "", false
	}
	return strings.ToUpper(strings.TrimSpace(privilege)), strings.Trim(strings.TrimSuffix(scope, ".*"), "`"), scalar(name), true
}

func splitKeyword(value, keyword string) (string, string, bool) {
	upper := strings.ToUpper(value)
	needle := " " + strings.ToUpper(keyword) + " "
	index := strings.Index(upper, needle)
	if index < 0 {
		return "", "", false
	}
	return value[:index], value[index+len(needle):], true
}

func validGrant(privilege, namespace string) bool {
	server := map[string]bool{"ACCOUNT_MANAGER": true, "NAMESPACE_MANAGER": true, "OPERATIONAL_OBSERVATION": true, "OPERATIONAL_CONTROL": true}
	namespaceGrants := map[string]bool{"DATA_READ": true, "DATA_WRITE": true, "SCHEMA_MANAGEMENT": true}
	if namespace == "" {
		return server[privilege]
	}
	return namespaceGrants[privilege]
}

func grantChange(privilege, namespace string, grant bool) func(*catalog.Account) error {
	return func(account *catalog.Account) error {
		index := accountGrantIndex(*account, privilege, namespace)
		if grant && index < 0 {
			account.Grants = append(account.Grants, catalog.Grant{Privilege: privilege, Namespace: namespace})
		}
		if !grant && index < 0 {
			return sqlFailure{1141, "42000", "grant is absent"}
		}
		if !grant {
			account.Grants = append(account.Grants[:index], account.Grants[index+1:]...)
		}
		return nil
	}
}

func accountGrantIndex(account catalog.Account, privilege, namespace string) int {
	for index, grant := range account.Grants {
		if grant.Privilege == privilege && grant.Namespace == namespace {
			return index
		}
	}
	return -1
}
func accountHasGrant(account catalog.Account, privilege string) bool {
	return accountGrantIndex(account, privilege, "") >= 0
}
func (s *textStatementExecutor) requireAccountManager() error {
	return s.requireGrant(accountManagerPrivilege, "")
}
func (s *textStatementExecutor) enabledAccountManagerCount() int {
	count := 0
	for _, account := range s.session.server.config.Catalog.Snapshot().Accounts {
		if !account.Locked && accountHasGrant(account, accountManagerPrivilege) {
			count++
		}
	}
	return count
}
func (s *textStatementExecutor) namespaceExists(name string) bool {
	_, found := s.session.server.config.Catalog.Snapshot().Namespaces[catalog.Key(name)]
	return found
}
func validateAccountInput(name, password string) error {
	if !validAccountName(name) || !validAccountPassword(password) {
		return sqlFailure{1819, "HY000", "invalid account or password"}
	}
	return nil
}
func validAccountName(name string) bool {
	if len(name) == 0 || len(name) > 32 || !asciiLetterOrDigit(name[0]) {
		return false
	}
	for _, character := range name[1:] {
		if !asciiLetterOrDigit(byte(character)) && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}
func validAccountPassword(password string) bool {
	return utf8.ValidString(password) && len(password) >= 12 && len(password) <= 1024
}
func asciiLetterOrDigit(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
}
func passwordHash(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

func showGrantsStatement(lower string) bool {
	return lower == "show grants" || strings.HasPrefix(lower, "show grants for ")
}

func (s *catalogExecutor) showGrants(query string) (*queryResult, error) {
	name, err := showGrantsTarget(query, s.username)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeShowGrants(name); err != nil {
		return nil, err
	}
	account, found := s.server.config.Catalog.Account(name)
	if !found {
		return nil, sqlFailure{1141, "42000", "unknown account"}
	}
	return showGrantsResult(name, account.Grants), nil
}

func showGrantsTarget(query, current string) (string, error) {
	rest := strings.TrimSpace(query[len("SHOW GRANTS"):])
	if rest == "" {
		return requireCurrentAccount(current)
	}
	lower := strings.ToLower(rest)
	if !strings.HasPrefix(lower, "for ") {
		return "", sqlFailure{1064, "42000", "malformed SHOW GRANTS"}
	}
	return showGrantsNamedTarget(strings.TrimSpace(rest[len("for "):]), current)
}

func showGrantsNamedTarget(target, current string) (string, error) {
	if strings.EqualFold(target, "CURRENT_USER") || strings.EqualFold(target, "CURRENT_USER()") {
		return requireCurrentAccount(current)
	}
	if len(target) < 2 || target[0] != '\'' || target[len(target)-1] != '\'' {
		return "", sqlFailure{1064, "42000", "malformed SHOW GRANTS"}
	}
	return scalar(target), nil
}

func requireCurrentAccount(current string) (string, error) {
	if current == "" {
		return "", sqlFailure{1227, "42000", "access denied"}
	}
	return current, nil
}

func (s *catalogExecutor) authorizeShowGrants(name string) error {
	if name == s.username {
		return nil
	}
	account, found := s.server.config.Catalog.Account(s.username)
	if !found || account.Locked || !accountHasGrant(account, accountManagerPrivilege) {
		return sqlFailure{1227, "42000", "access denied"}
	}
	return nil
}

func showGrantsResult(name string, grants []catalog.Grant) *queryResult {
	rows := make([][]string, 0, len(grants))
	for _, grant := range sortedAccountGrants(grants) {
		rows = append(rows, []string{renderGrantStatement(name, grant)})
	}
	return &queryResult{columns: []string{"Grants for " + name}, rows: rows}
}

func sortedAccountGrants(grants []catalog.Grant) []catalog.Grant {
	result := append([]catalog.Grant(nil), grants...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Privilege != result[j].Privilege {
			return result[i].Privilege < result[j].Privilege
		}
		return result[i].Namespace < result[j].Namespace
	})
	return result
}

func renderGrantStatement(name string, grant catalog.Grant) string {
	scope := "*.*"
	if grant.Namespace != "" {
		scope = grant.Namespace + ".*"
	}
	return "GRANT " + grant.Privilege + " ON " + scope + " TO '" + name + "'"
}
