package mysql

import "strings"

// statementExecutionPolicy owns the common rules for one SQL statement. The
// protocol-facing text query adapter enters this module before a statement
// implementation runs.
type statementExecutionPolicy struct{ executor *textStatementExecutor }

// normalizedStatement is the common policy input for text and prepared SQL.
// Prepared values become SQL text before this value is made.
type normalizedStatement struct {
	query string
	lower string
}

func newStatementExecutionPolicy(executor *textStatementExecutor) statementExecutionPolicy {
	return statementExecutionPolicy{executor: executor}
}

func normalizeStatement(query string) (normalizedStatement, error) {
	statement := normalizeStatementText(query)
	if statement.query == "" {
		return normalizedStatement{}, sqlFailure{1065, "42000", "query was empty"}
	}
	return statement, nil
}

func normalizeStatementText(query string) normalizedStatement {
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	query = stripLeadingSQLComments(query)
	return normalizedStatement{query: query, lower: strings.ToLower(query)}
}

func (p statementExecutionPolicy) execute(statement normalizedStatement) (*queryResult, error) {
	s := p.executor
	query, lower := statement.query, statement.lower
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
