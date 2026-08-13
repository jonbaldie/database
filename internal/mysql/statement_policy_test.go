package mysql

// executeStatement keeps internal statement implementation tests on the same
// policy entry as the MySQL wire protocol.
func executeStatement(executor *textStatementExecutor, query string) (*queryResult, error) {
	statement, err := normalizeStatement(query)
	if err != nil {
		return nil, err
	}
	return newStatementExecutionPolicy(executor).execute(statement)
}
