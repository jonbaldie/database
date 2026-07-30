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
	"rollback":          rollbackTransactionCommand,
}

var prefixTransactionCommands = []struct {
	prefix  string
	command transactionCommand
}{
	{"begin ", beginTransactionCommand},
	{"start transaction ", beginTransactionCommand},
	{"savepoint ", savepointCommand},
	{"rollback to savepoint ", rollbackToSavepointCommand},
	{"release savepoint ", releaseSavepointCommand},
}

func findTransactionHandler(lower string) (transactionCommand, bool) {
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
	return (&transactionExecutor{s}).commit()
}

func rollbackTransactionCommand(s *session, _ string) error {
	return (&transactionExecutor{s}).rollback()
}

func savepointCommand(s *session, query string) error {
	return (&transactionExecutor{s}).save(query[len("SAVEPOINT "):])
}

func rollbackToSavepointCommand(s *session, query string) error {
	return (&transactionExecutor{s}).rollbackTo(query[len("ROLLBACK TO SAVEPOINT "):])
}

func releaseSavepointCommand(s *session, query string) error {
	return (&transactionExecutor{s}).release(query[len("RELEASE SAVEPOINT "):])
}

type statementTransaction struct {
	transactional bool
	autocommit    bool
}

func (s *textStatementExecutor) executeWithTransaction(query, lower string) (*queryResult, error) {
	if isTransactionControl(lower) || isSettingControl(lower) {
		return s.dispatchStatement(query, lower)
	}
	execution, err := s.beginStatementTransaction(lower)
	if err != nil {
		return nil, err
	}
	if !execution.transactional {
		return s.dispatchStatement(query, lower)
	}
	defer s.clearStatementDefinition()
	result, err := s.dispatchStatement(query, lower)
	return execution.finish(s.session, result, err)
}

func (s *textStatementExecutor) beginStatementTransaction(lower string) (statementTransaction, error) {
	if !isTransactionalStatement(lower) {
		return statementTransaction{}, nil
	}
	dataDefinition := isDataDefinition(lower)
	if err := s.commitBeforeDefinition(dataDefinition); err != nil {
		return statementTransaction{}, err
	}
	autocommit, err := s.startStatementTransaction(dataDefinition, isMutationStatement(lower))
	if err != nil {
		return statementTransaction{}, err
	}
	if err := s.prepareStatementDefinition(isMutationStatement(lower)); err != nil {
		return statementTransaction{}, err
	}
	return statementTransaction{transactional: true, autocommit: autocommit}, nil
}

func (s *textStatementExecutor) commitBeforeDefinition(dataDefinition bool) error {
	if !dataDefinition || !s.transaction {
		return nil
	}
	if s.transactionReadOnly {
		return readOnlyTransactionFailure()
	}
	return (&transactionExecutor{s.session}).commit()
}

func (s *textStatementExecutor) startStatementTransaction(dataDefinition, mutation bool) (bool, error) {
	if s.transaction {
		if s.transactionReadOnly && mutation {
			return false, readOnlyTransactionFailure()
		}
		return false, nil
	}
	s.beginTransaction(s.nextIsolation, s.nextReadOnly)
	s.consumeNextCharacteristics()
	return !s.autocommitOff || dataDefinition, nil
}

func (e statementTransaction) finish(s *session, result *queryResult, err error) (*queryResult, error) {
	if err != nil {
		return e.abort(s, err)
	}
	if !e.autocommit {
		return result, nil
	}
	if err := (&transactionExecutor{s}).commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (e statementTransaction) abort(s *session, err error) (*queryResult, error) {
	if e.autocommit {
		_ = rollbackTransaction(s)
	}
	return nil, err
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
	if strings.HasPrefix(lower, "set ") {
		if handled, err := s.setTimeZone(query); handled {
			return nil, true, err
		}
	}
	return nil, true, s.applySetting(query, lower)
}

func (s *textStatementExecutor) applySetting(_ string, lower string) error {
	if strings.HasPrefix(lower, "reset ") {
		return sqlFailure{1235, "42000", "unsupported session reset"}
	}
	normalized := strings.Join(strings.Fields(lower), " ")
	if handled, err := s.applyAutocommitSetting(normalized); handled {
		return err
	}
	if handled, err := s.applyIsolationSetting(normalized); handled {
		return err
	}
	if handled, err := s.applyReadOnlySetting(normalized); handled {
		return err
	}
	return sqlFailure{1193, "HY000", "unknown or unsupported session setting"}
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
	if !off && s.transaction {
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
	if s.transaction {
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
	lower := strings.ToLower(strings.TrimSpace(query))
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
	s.transaction = true
	s.transactionSnapshot = catalog.Definition{}
	s.transactionRevision = 0
	s.transactionStateSet = false
	s.transactionReadSet = false
	s.transactionDirty = false
	s.transactionIsolation = isolation
	s.transactionReadOnly = readOnly
	s.savepoints = make(map[string]catalog.Definition)
	s.savepointDirty = make(map[string]bool)
	s.savepointMutations = make(map[string]int)
	s.savepointRead = make(map[string]bool)
	s.transactionMutations = nil
}

func (s *session) consumeNextCharacteristics() {
	s.nextIsolation, s.nextReadOnly = s.isolation, s.readOnly
}

func (s *session) prepareStatementDefinition(forWrite bool) error {
	if !s.transaction {
		return nil
	}
	if err := s.ensureWorkingDefinition(); err != nil {
		return err
	}
	if !forWrite {
		s.transactionReadSet = true
	}
	s.statementDefinition = s.statementDefinitionFor(forWrite)
	s.statementDefinitionSet = true
	return nil
}

func (s *session) statementDefinitionFor(forWrite bool) catalog.Definition {
	if forWrite || s.transactionDirty || s.transactionIsolation == isolationRepeatableRead && s.transactionReadSet {
		return s.transactionSnapshot
	}
	if s.server.config.Catalog == nil {
		return emptyDefinition()
	}
	return s.server.config.Catalog.Snapshot()
}

func (s *session) clearStatementDefinition() {
	s.statementDefinition = catalog.Definition{}
	s.statementDefinitionSet = false
}

func (s *session) ensureWorkingDefinition() error {
	if !s.transaction {
		return nil
	}
	if s.transactionStateSet {
		if s.transactionDirty && (s.transactionIsolation == isolationReadCommitted || !s.transactionReadSet) {
			return s.refreshWorkingDefinition()
		}
		return nil
	}
	if s.server.config.Catalog == nil {
		s.transactionSnapshot = emptyDefinition()
		s.transactionRevision = 0
		s.transactionStateSet = true
		return nil
	}
	definition, revision := s.server.config.Catalog.SnapshotWithRevision()
	s.transactionSnapshot, s.transactionRevision = definition, revision
	s.transactionStateSet = true
	return nil
}

func (s *session) refreshWorkingDefinition() error {
	if s.server.config.Catalog == nil {
		s.transactionSnapshot = emptyDefinition()
		s.transactionRevision = 0
		s.transactionStateSet = true
		return nil
	}
	definition, revision := s.server.config.Catalog.SnapshotWithRevision()
	for _, mutation := range s.transactionMutations {
		staged, err := catalog.Apply(definition, mutation)
		if err != nil {
			return err
		}
		definition = staged
	}
	s.transactionSnapshot, s.transactionRevision = definition, revision
	s.transactionStateSet = true
	return nil
}

func (s *session) currentDefinition() catalog.Definition {
	if s.statementDefinitionSet {
		return s.statementDefinition
	}
	if s.transaction {
		_ = s.ensureWorkingDefinition()
		if s.transactionDirty || s.transactionIsolation == isolationRepeatableRead {
			return s.transactionSnapshot
		}
		if s.server.config.Catalog == nil {
			return emptyDefinition()
		}
		return s.server.config.Catalog.Snapshot()
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
	if s.transactionReadOnly {
		return readOnlyTransactionFailure()
	}
	if s.server.config.Catalog == nil {
		return sqlFailure{1105, "HY000", "database is not initialized"}
	}
	if s.transaction {
		return s.mutateTransactionCatalog(action)
	}
	return s.mutateDurableCatalog(action)
}

func (s *session) mutateTransactionCatalog(action func(*catalog.Definition) error) error {
	if err := s.ensureWorkingDefinition(); err != nil {
		return err
	}
	staged, err := catalog.Apply(s.transactionSnapshot, action)
	if err != nil {
		return err
	}
	if err := validateConstraintDefinition(s.transactionSnapshot, staged); err != nil {
		return err
	}
	s.transactionSnapshot = staged
	s.transactionDirty = true
	s.transactionMutations = append(s.transactionMutations, action)
	return nil
}

func (s *session) mutateDurableCatalog(action func(*catalog.Definition) error) error {
	definition, revision := s.server.config.Catalog.SnapshotWithRevision()
	staged, err := catalog.Apply(definition, action)
	if err != nil {
		return err
	}
	if err := validateConstraintDefinition(definition, staged); err != nil {
		return err
	}
	if err := s.server.config.Catalog.ReplaceIfRevision(revision, staged); err != nil {
		if errors.Is(err, catalog.ErrRevisionConflict) {
			return sqlFailure{1213, "40001", "catalog changed concurrently; try restarting the transaction"}
		}
		return err
	}
	return nil
}

func catalogMutationFailure(err error, fallback sqlFailure) error {
	var failure sqlFailure
	if errors.As(err, &failure) {
		return err
	}
	fallback.message = err.Error()
	return fallback
}

func (s *session) databaseExists(name string) error {
	if strings.EqualFold(name, informationSchemaName) {
		return nil
	}
	if _, found := s.currentDefinition().Namespaces[catalog.Key(name)]; !found {
		return sqlFailure{1049, "42000", "unknown database '" + name + "'"}
	}
	return nil
}

func (s *transactionExecutor) commit() error {
	if !s.transaction {
		return nil
	}
	if s.transactionDirty && s.server.config.Catalog != nil {
		if err := s.server.config.Catalog.ReplaceIfRevision(s.transactionRevision, s.transactionSnapshot); err != nil {
			s.finishTransaction()
			if errors.Is(err, catalog.ErrRevisionConflict) {
				return sqlFailure{1213, "40001", "Deadlock found when trying to get lock; try restarting transaction"}
			}
			return sqlFailure{1105, "HY000", err.Error()}
		}
	}
	s.finishTransaction()
	return nil
}

func (s *session) finishTransaction() {
	s.transaction = false
	s.transactionSnapshot = catalog.Definition{}
	s.transactionRevision = 0
	s.transactionStateSet = false
	s.transactionReadSet = false
	s.transactionDirty = false
	s.transactionIsolation = isolationRepeatableRead
	s.transactionReadOnly = false
	s.savepoints = make(map[string]catalog.Definition)
	s.savepointDirty = make(map[string]bool)
	s.savepointMutations = make(map[string]int)
	s.savepointRead = make(map[string]bool)
	s.transactionMutations = nil
}

func (s *transactionExecutor) save(value string) error {
	if !s.transaction {
		return sqlFailure{1196, "HY000", "no active transaction"}
	}
	if err := s.ensureWorkingDefinition(); err != nil {
		return err
	}
	if s.savepoints == nil {
		s.savepoints = make(map[string]catalog.Definition)
	}
	if s.savepointDirty == nil {
		s.savepointDirty = make(map[string]bool)
	}
	if s.savepointMutations == nil {
		s.savepointMutations = make(map[string]int)
	}
	if s.savepointRead == nil {
		s.savepointRead = make(map[string]bool)
	}
	name := identifier(strings.TrimSpace(value))
	s.savepoints[catalog.Key(name)] = s.transactionSnapshot
	s.savepointDirty[catalog.Key(name)] = s.transactionDirty
	s.savepointMutations[catalog.Key(name)] = len(s.transactionMutations)
	s.savepointRead[catalog.Key(name)] = s.transactionReadSet
	return nil
}

func (s *transactionExecutor) rollbackTo(value string) error {
	if !s.transaction {
		return sqlFailure{1305, "42000", "savepoint does not exist"}
	}
	name := identifier(strings.TrimSpace(value))
	key := catalog.Key(name)
	snapshot, found := s.savepoints[key]
	if !found {
		return sqlFailure{1305, "42000", "savepoint does not exist"}
	}
	s.transactionSnapshot = snapshot
	s.transactionStateSet = true
	s.transactionDirty = s.savepointDirty[key]
	s.transactionReadSet = s.savepointRead[key]
	mutationCount := s.savepointMutations[key]
	if mutationCount < len(s.transactionMutations) {
		s.transactionMutations = append([]func(*catalog.Definition) error(nil), s.transactionMutations[:mutationCount]...)
	}
	return nil
}

func (s *transactionExecutor) release(value string) error {
	name := identifier(strings.TrimSpace(value))
	key := catalog.Key(name)
	if _, found := s.savepoints[key]; !found {
		return sqlFailure{1305, "42000", "savepoint does not exist"}
	}
	delete(s.savepoints, key)
	delete(s.savepointDirty, key)
	delete(s.savepointMutations, key)
	delete(s.savepointRead, key)
	return nil
}

func (s *transactionExecutor) rollback() error {
	if s.transaction {
		s.finishTransaction()
	}
	return nil
}

func readOnlyTransactionFailure() error {
	return sqlFailure{1792, "HY000", "Cannot execute statement in a READ ONLY transaction"}
}

func rollbackTransaction(s *session) error {
	if s.transaction {
		s.finishTransaction()
	}
	return nil
}
