package mysql

import (
	"errors"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

type isolationLevel uint8

const (
	isolationRepeatableRead isolationLevel = iota
	isolationReadCommitted
)

type transactionCommand func(*session, string) error

var exactTransactionCommands = map[string]transactionCommand{
	"begin":             beginTransactionCommand,
	"start transaction": beginTransactionCommand,
	"commit":            commitTransactionCommand,
	"commit work":       commitTransactionCommand,
	"rollback":          rollbackTransactionCommand,
	"rollback work":     rollbackTransactionCommand,
}

var prefixTransactionCommands = []struct {
	prefix  string
	command transactionCommand
}{
	{"begin ", beginTransactionCommand},
	{"start transaction ", beginTransactionCommand},
	{"savepoint ", savepointCommand},
	{"rollback work to ", rollbackToSavepointCommand},
	{"rollback to ", rollbackToSavepointCommand},
	{"release savepoint ", releaseSavepointCommand},
}

func compactSQLKeywords(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func findTransactionHandler(lower string) (transactionCommand, bool) {
	lower = compactSQLKeywords(lower)
	if command := exactTransactionCommands[lower]; command != nil {
		return command, true
	}
	for _, candidate := range prefixTransactionCommands {
		if strings.HasPrefix(lower, candidate.prefix) {
			return candidate.command, true
		}
	}
	return nil, false
}

func beginTransactionCommand(s *session, query string) error {
	return (&transactionExecutor{s}).begin(query)
}

func commitTransactionCommand(s *session, _ string) error {
	return (&transactionExecutor{s}).commitTransactionControl()
}

func rollbackTransactionCommand(s *session, _ string) error {
	return (&transactionExecutor{s}).rollback()
}

func savepointCommand(s *session, query string) error {
	return (&transactionExecutor{s}).save(stripTransactionKeywords(query, "SAVEPOINT"))
}

func rollbackToSavepointCommand(s *session, query string) error {
	suffix := strings.TrimSpace(query[len("ROLLBACK"):])
	lower := strings.ToLower(suffix)
	if strings.HasPrefix(lower, "work ") {
		suffix = strings.TrimSpace(suffix[len("WORK "):])
		lower = strings.ToLower(suffix)
	}
	if !strings.HasPrefix(lower, "to ") {
		return sqlFailure{1064, "42000", "invalid rollback to savepoint statement"}
	}
	suffix = strings.TrimSpace(suffix[len("TO "):])
	if strings.HasPrefix(strings.ToLower(suffix), "savepoint ") {
		suffix = strings.TrimSpace(suffix[len("SAVEPOINT "):])
	}
	return (&transactionExecutor{s}).rollbackTo(suffix)
}

func releaseSavepointCommand(s *session, query string) error {
	return (&transactionExecutor{s}).release(stripTransactionKeywords(query, "RELEASE", "SAVEPOINT"))
}

func stripTransactionKeywords(query string, keywords ...string) string {
	fields := strings.Fields(query)
	if len(fields) < len(keywords) {
		return strings.TrimSpace(query)
	}
	for index, keyword := range keywords {
		if !strings.EqualFold(fields[index], keyword) {
			return strings.TrimSpace(query)
		}
	}
	return strings.Join(fields[len(keywords):], " ")
}

type statementTransaction struct {
	transactional bool
	autocommit    bool
}

func isImmediateStatement(lower string) bool {
	return isTransactionControl(lower) || isSettingControl(lower) || accountAdministrationStatement(lower)
}

func (s *textStatementExecutor) dispatchAndCheckResources(query, lower string) (*queryResult, error) {
	result, err := s.dispatchStatement(query, lower)
	if err != nil {
		return result, err
	}
	return result, s.session.checkStatementResources()
}

func (s *textStatementExecutor) beginStatementTransaction(lower string) (statementTransaction, error) {
	if !isTransactionalStatement(lower) {
		return statementTransaction{}, nil
	}
	dataDefinition := isDataDefinition(lower)
	if err := s.commitBeforeDefinition(dataDefinition); err != nil {
		return statementTransaction{}, err
	}
	autocommit, err := s.startStatementTransaction(dataDefinition, isMutationStatement(lower) || isLockingReadStatement(lower))
	if err != nil {
		return statementTransaction{}, err
	}
	if err := s.prepareStatementDefinition(isMutationStatement(lower)); err != nil {
		return statementTransaction{}, err
	}
	return statementTransaction{transactional: true, autocommit: autocommit}, nil
}

func isLockingReadStatement(lower string) bool {
	_, locking, _ := splitLockingRead(lower)
	return locking != nil
}

func (s *textStatementExecutor) commitBeforeDefinition(dataDefinition bool) error {
	if !dataDefinition || !s.inTransaction() {
		return nil
	}
	if s.transactionReadOnly {
		return readOnlyTransactionFailure()
	}
	return (&transactionExecutor{s.session}).commit()
}

func (s *textStatementExecutor) startStatementTransaction(dataDefinition, mutation bool) (bool, error) {
	if started, err, handled := s.continueOpenTransaction(mutation); handled {
		return started, err
	}
	if s.nextReadOnly && mutation {
		return false, readOnlyTransactionFailure()
	}
	return s.beginAutocommitOrTransaction(dataDefinition, mutation), nil
}

func (s *textStatementExecutor) continueOpenTransaction(mutation bool) (bool, error, bool) {
	if !s.inTransaction() {
		return false, nil, false
	}
	if s.transactionReadOnly && mutation {
		return false, readOnlyTransactionFailure(), true
	}
	return false, nil, true
}

func (s *textStatementExecutor) beginAutocommitOrTransaction(dataDefinition, mutation bool) bool {
	autocommit := !s.autocommitOff || dataDefinition
	// Autocommit DML publishes through ApplyDurable directly. Starting a
	// statement transaction would snapshot, stage, and re-apply the same
	// mutation before the coalesced writer ever sees it.
	if autocommit && mutation && !dataDefinition {
		s.consumeNextCharacteristics()
		return true
	}
	s.beginTransaction(s.nextIsolation, s.nextReadOnly)
	s.consumeNextCharacteristics()
	return autocommit
}

func (e statementTransaction) finish(s *session, result *queryResult, err error) (*queryResult, error) {
	if err != nil {
		return e.abort(s, err)
	}
	if !e.autocommit {
		return result, nil
	}
	if s.inTransaction() {
		if err := (&transactionExecutor{s}).commitAutocommit(); err != nil {
			return nil, err
		}
		return result, nil
	}
	releaseSessionLocks(s)
	return result, nil
}

func (e statementTransaction) abort(s *session, err error) (*queryResult, error) {
	if e.autocommit || isDeadlock(err) || isCancellation(err) {
		if s.inTransaction() {
			_ = rollbackTransaction(s)
		} else {
			releaseSessionLocks(s)
		}
	}
	return nil, err
}

func releaseSessionLocks(s *session) {
	if s != nil && s.server != nil && s.server.locks != nil {
		s.server.locks.release(s)
	}
}

func isDeadlock(err error) bool {
	var failure sqlFailure
	return errors.As(err, &failure) && failure.code == 1213 && failure.state == "40001"
}

func isCancellation(err error) bool {
	var failure sqlFailure
	return errors.As(err, &failure) && failure.code == 1317 && failure.state == "70100"
}

func (s *textStatementExecutor) dispatchStatement(query, lower string) (*queryResult, error) {
	for _, handler := range s.statementHandlers() {
		result, handled, err := handler(query, lower)
		if handled {
			return result, err
		}
	}
	return nil, sqlFailure{1064, "42000", "unsupported query: " + query}
}

func isTransactionControl(lower string) bool {
	_, handled := findTransactionHandler(lower)
	return handled
}

func isSettingControl(lower string) bool {
	return strings.HasPrefix(lower, "set ") || strings.HasPrefix(lower, "reset ")
}

func isDataDefinition(lower string) bool {
	return strings.HasPrefix(lower, "create database ") || strings.HasPrefix(lower, "create schema ") || strings.HasPrefix(lower, "create table ") ||
		strings.HasPrefix(lower, "drop database ") || strings.HasPrefix(lower, "drop schema ") || strings.HasPrefix(lower, "drop table ") ||
		strings.HasPrefix(lower, "truncate table ") || strings.HasPrefix(lower, "rename table ") || strings.HasPrefix(lower, "alter table ")
}

func isMutationStatement(lower string) bool {
	return strings.HasPrefix(lower, "insert into ") || strings.HasPrefix(lower, "replace ") || strings.HasPrefix(lower, "update ") || strings.HasPrefix(lower, "delete from ") || isDataDefinition(lower)
}

func isTransactionalStatement(lower string) bool {
	return isComposedSelectStatement(lower) || strings.HasPrefix(lower, "show ") || strings.HasPrefix(lower, "explain ") || isMutationStatement(lower)
}

func (s *textStatementExecutor) settingStatement(query, lower string) (*queryResult, bool, error) {
	if !isSettingControl(lower) {
		return nil, false, nil
	}
	return nil, true, s.applySetting(query, lower)
}

func (s *textStatementExecutor) applySetting(query string, lower string) error {
	if strings.HasPrefix(lower, "reset ") {
		return sqlFailure{1235, "42000", "unsupported session reset"}
	}
	normalized := strings.Join(strings.Fields(lower), " ")
	if _, _, matched := transactionSetting(normalized); matched {
		if s.session.inTransaction() {
			return sqlFailure{1568, "25001", "transaction characteristics cannot change in an active transaction"}
		}
		if handled, err := s.applyIsolationSetting(normalized); handled {
			return err
		}
		if handled, err := s.applyReadOnlySetting(normalized); handled {
			return err
		}
		return sqlFailure{1231, "42000", "unsupported transaction setting"}
	}
	return s.applySessionAssignments(query)
}

func (s *session) applyAutocommitSetting(normalized string) (bool, error) {
	compact := strings.ReplaceAll(normalized, " ", "")
	compact = strings.ReplaceAll(compact, "@@", "")
	prefix := autocommitSettingPrefix(compact)
	if prefix == "" {
		return false, nil
	}
	value := compact[len(prefix):]
	off, valid := map[string]bool{"0": true, "off": true, "false": true, "1": false, "on": false, "true": false}[value]
	if !valid {
		return true, sqlFailure{1231, "42000", "autocommit has an invalid value"}
	}
	if !off && s.inTransaction() {
		if err := (&transactionExecutor{s}).commit(); err != nil {
			return true, err
		}
	}
	s.autocommitOff = off
	return true, nil
}

func autocommitSettingPrefix(compact string) string {
	for _, prefix := range []string{"setautocommit=", "setsessionautocommit="} {
		if strings.HasPrefix(compact, prefix) {
			return prefix
		}
	}
	return ""
}

func (s *session) applyIsolationSetting(normalized string) (bool, error) {
	session, setting, matched := transactionSetting(normalized)
	if !matched || !strings.HasPrefix(setting, "isolation level ") {
		return false, nil
	}
	level, err := parseIsolationLevel(strings.TrimPrefix(setting, "isolation level "))
	if err != nil {
		return true, err
	}
	if session {
		s.isolation = level
	}
	s.nextIsolation = level
	return true, nil
}

func (s *session) applyReadOnlySetting(normalized string) (bool, error) {
	session, setting, matched := transactionSetting(normalized)
	if !matched {
		return false, nil
	}
	readOnly, matched := map[string]bool{"read only": true, "read write": false}[setting]
	if !matched {
		return false, nil
	}
	if session {
		s.readOnly = readOnly
	}
	s.nextReadOnly = readOnly
	return true, nil
}

func transactionSetting(normalized string) (bool, string, bool) {
	rest := strings.TrimSpace(strings.TrimPrefix(normalized, "set"))
	session := strings.HasPrefix(rest, "session ")
	if session {
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "session"))
	}
	if !strings.HasPrefix(rest, "transaction ") {
		return false, "", false
	}
	return session, strings.TrimSpace(strings.TrimPrefix(rest, "transaction")), true
}

func parseIsolationLevel(value string) (isolationLevel, error) {
	switch strings.Trim(strings.ToLower(value), "'\"") {
	case "read committed":
		return isolationReadCommitted, nil
	case "repeatable read":
		return isolationRepeatableRead, nil
	case "read uncommitted", "serializable":
		return isolationRepeatableRead, sqlFailure{1231, "42000", "unsupported transaction isolation level"}
	default:
		return isolationRepeatableRead, sqlFailure{1231, "42000", "unsupported transaction isolation level"}
	}
}

func (s *transactionExecutor) begin(query string) error {
	isolation, readOnly, err := transactionStartOptions(query, s.nextIsolation, s.nextReadOnly)
	if err != nil {
		return err
	}
	if s.inTransaction() {
		if err := s.commit(); err != nil {
			return err
		}
	}
	s.beginTransaction(isolation, readOnly)
	s.consumeNextCharacteristics()
	return nil
}

func transactionStartOptions(query string, defaultIsolation isolationLevel, defaultReadOnly bool) (isolationLevel, bool, error) {
	suffix, ok := transactionStartSuffix(query)
	if !ok {
		return defaultIsolation, defaultReadOnly, sqlFailure{1064, "42000", "malformed transaction start"}
	}
	return transactionStartOption(stripConsistentSnapshot(suffix), defaultIsolation, defaultReadOnly)
}

func transactionStartSuffix(query string) (string, bool) {
	lower := compactSQLKeywords(strings.ToLower(query))
	if lower == "begin" || lower == "start transaction" {
		return "", true
	}
	for _, prefix := range []string{"begin ", "start transaction "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(lower[len(prefix):]), true
		}
	}
	return "", false
}

func stripConsistentSnapshot(suffix string) string {
	if !strings.HasPrefix(suffix, "with consistent snapshot") {
		return suffix
	}
	suffix = strings.TrimSpace(strings.TrimPrefix(suffix, "with consistent snapshot"))
	return strings.TrimSpace(strings.TrimPrefix(suffix, ","))
}

func transactionStartOption(suffix string, defaultIsolation isolationLevel, defaultReadOnly bool) (isolationLevel, bool, error) {
	if suffix == "" || suffix == "work" {
		return defaultIsolation, defaultReadOnly, nil
	}
	if readOnly, ok := map[string]bool{"read only": true, "read write": false}[suffix]; ok {
		return defaultIsolation, readOnly, nil
	}
	return defaultIsolation, defaultReadOnly, sqlFailure{1064, "42000", "unsupported transaction start option"}
}

func (s *session) beginTransaction(isolation isolationLevel, readOnly bool) {
	s.catalogTxn = catalog.BeginTxn(s.server.config.Catalog, isolation.catalogIsolation())
	s.transactionReadOnly = readOnly
}

func (s *session) inTransaction() bool {
	return s.catalogTxn != nil
}

func (l isolationLevel) catalogIsolation() catalog.Isolation {
	if l == isolationReadCommitted {
		return catalog.ReadCommitted
	}
	return catalog.RepeatableRead
}

func (s *session) consumeNextCharacteristics() {
	s.nextIsolation, s.nextReadOnly = s.isolation, s.readOnly
}

func (s *session) prepareStatementDefinition(forWrite bool) error {
	if !s.inTransaction() {
		return nil
	}
	definition, err := s.catalogTxn.StatementView(forWrite)
	if err != nil {
		return err
	}
	s.statementDefinition = definition
	s.statementDefinitionSet = true
	return nil
}

func (s *session) clearStatementDefinition() {
	s.statementDefinition = catalog.Definition{}
	s.statementDefinitionSet = false
}

func (s *session) currentDefinition() catalog.Definition {
	if s.statementDefinitionSet {
		return s.statementDefinition
	}
	if s.inTransaction() {
		return s.catalogTxn.Current()
	}
	if s.server.config.Catalog == nil {
		return emptyDefinition()
	}
	return s.server.config.Catalog.Snapshot()
}

func emptyDefinition() catalog.Definition {
	return catalog.Definition{Namespaces: map[string]catalog.Namespace{}}
}

func (s *session) mutateCatalog(action func(*catalog.Definition) error) error {
	if err := s.checkStatementResources(); err != nil {
		return err
	}
	if s.transactionReadOnly {
		return readOnlyTransactionFailure()
	}
	if s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if s.inTransaction() {
		return s.mutateTransactionCatalog(action)
	}
	return s.mutateDurableCatalog(action)
}

func (s *session) mutateTransactionCatalog(action func(*catalog.Definition) error) error {
	return s.catalogTxn.Stage(action, s.checkStatementResources)
}

func (s *session) mutateDurableCatalog(action func(*catalog.Definition) error) error {
	if err := s.checkStatementResources(); err != nil {
		return err
	}
	return s.server.config.Catalog.ApplyDurable(func(base catalog.Definition) (catalog.Definition, error) {
		if err := action(&base); err != nil {
			return catalog.Definition{}, err
		}
		if err := s.checkStatementResources(); err != nil {
			return catalog.Definition{}, err
		}
		return base, nil
	})
}

// catalogMutationFailure maps a typed catalog error to its MySQL error. Any
// other catalog error keeps the statement's fallback code with its own text.
func catalogMutationFailure(err error, fallback sqlFailure) error {
	var failure sqlFailure
	if errors.As(err, &failure) {
		return err
	}
	if errors.Is(err, catalog.ErrRevisionConflict) {
		return sqlFailure{1213, "40001", "Deadlock found when trying to get lock; try restarting transaction"}
	}
	if errors.Is(err, catalog.ErrDuplicateKey) {
		return sqlFailure{1062, "23000", "Duplicate entry for key 'PRIMARY'"}
	}
	fallback.message = err.Error()
	return fallback
}

func (s *session) databaseExists(name string) error {
	return resolveNamespace(s.currentDefinition(), s.username, name).requireDefinition()
}

func (s *transactionExecutor) commit() error {
	return s.commitWithPublicationBoundary(false)
}

func (s *transactionExecutor) commitTransactionControl() error {
	return s.commitWithPublicationBoundary(true)
}

// commitAutocommit publishes one statement's staged mutations against the latest
// catalog without an optimistic revision gate. Concurrent autocommit writers that
// started at the same snapshot must all succeed when their keys do not conflict.
func (s *transactionExecutor) commitAutocommit() error {
	return s.commitPublishedMutations(false, false)
}

func (s *transactionExecutor) commitWithPublicationBoundary(finalize bool) error {
	return s.commitPublishedMutations(true, finalize)
}

func (s *transactionExecutor) commitPublishedMutations(requireRevision, finalize bool) error {
	if !s.inTransaction() {
		return nil
	}
	if err := s.checkStatementResources(); err != nil {
		return err
	}
	published := s.catalogTxn.Dirty() && s.server.config.Catalog != nil
	commit := s.catalogTxn.CommitAtLatest
	if requireRevision {
		commit = s.catalogTxn.Commit
	}
	if err := commit(); err != nil {
		s.finishTransaction()
		return catalogMutationFailure(err, sqlFailure{1105, "HY000", err.Error()})
	}
	if published && finalize {
		finalizeStatementResources(s.resources)
	}
	s.finishTransaction()
	return nil
}

func (s *session) finishTransaction() {
	if s.server != nil && s.server.locks != nil {
		s.server.locks.release(s)
	}
	if s.catalogTxn != nil {
		s.catalogTxn.Abort()
	}
	s.catalogTxn = nil
	s.transactionReadOnly = false
}

func (s *transactionExecutor) save(value string) error {
	if !s.inTransaction() {
		return sqlFailure{1196, "HY000", "no active transaction"}
	}
	name, err := parseSavepointName(value)
	if err != nil {
		return err
	}
	return s.catalogTxn.Savepoint(name)
}

func (s *transactionExecutor) rollbackTo(value string) error {
	if !s.inTransaction() {
		return savepointMissingFailure()
	}
	name, err := parseSavepointName(value)
	if err != nil {
		return err
	}
	return savepointFailure(s.catalogTxn.RollbackTo(name))
}

func (s *transactionExecutor) release(value string) error {
	name, err := parseSavepointName(value)
	if err != nil {
		return err
	}
	if !s.inTransaction() {
		return savepointMissingFailure()
	}
	return savepointFailure(s.catalogTxn.Release(name))
}

func savepointFailure(err error) error {
	if errors.Is(err, catalog.ErrSavepointNotFound) {
		return savepointMissingFailure()
	}
	return err
}

func savepointMissingFailure() error {
	return sqlFailure{1305, "42000", "savepoint does not exist"}
}

func parseSavepointName(value string) (string, error) {
	name, remainder, ok := consumeIdentifier(value)
	if !ok || strings.TrimSpace(remainder) != "" {
		return "", sqlFailure{1064, "42000", "invalid savepoint name"}
	}
	if err := validateIdentifierLength(name); err != nil {
		return "", err
	}
	return name, nil
}

func (s *transactionExecutor) rollback() error {
	if s.inTransaction() {
		s.finishTransaction()
	}
	return nil
}

func readOnlyTransactionFailure() error {
	return sqlFailure{1792, "HY000", "Cannot execute statement in a READ ONLY transaction"}
}

func rollbackTransaction(s *session) error {
	if s.inTransaction() {
		s.finishTransaction()
	}
	return nil
}
