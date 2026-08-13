package mysql

// statementExecutionPolicy owns the common rules for one SQL statement. The
// protocol-facing text query adapter enters this module before a statement
// implementation runs.
type statementExecutionPolicy struct{ executor *textStatementExecutor }

func newStatementExecutionPolicy(executor *textStatementExecutor) statementExecutionPolicy {
	return statementExecutionPolicy{executor: executor}
}

func (p statementExecutionPolicy) execute(query, lower string) (*queryResult, error) {
	s := p.executor
	if err := s.session.checkStatementResources(); err != nil {
		return nil, err
	}
	if err := s.authorizeStatement(lower); err != nil {
		return nil, err
	}
	if isImmediateStatement(lower) {
		// Transaction controls and session settings may publish an irreversible
		// commit (for example, COMMIT or SET autocommit = 1). Admission is their
		// final resource check so a later event cannot misreport that publication
		// as a rejected statement.
		return s.dispatchStatement(query, lower)
	}
	execution, err := s.beginStatementTransaction(lower)
	if err != nil {
		return nil, err
	}
	if !execution.transactional {
		return s.dispatchAndCheckResources(query, lower)
	}
	defer s.clearStatementDefinition()
	result, err := s.dispatchStatement(query, lower)
	// A mutation may already be staged inside an explicit transaction. Its
	// catalog path checks immediately before staging, so do not turn a later
	// post-dispatch deadline observation into a failed statement with retained
	// staged changes. Autocommit mutations are safe to check before finish
	// because finish will roll them back on error.
	if err == nil && (execution.autocommit || !isMutationStatement(lower)) {
		err = s.session.checkStatementResources()
	}
	return execution.finish(s.session, result, err)
}
