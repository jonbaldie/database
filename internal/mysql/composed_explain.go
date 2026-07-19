package mysql

import (
	"strings"

	"github.com/jonbaldie/database/internal/queryexplanation"
)

func (s *textStatementExecutor) explainComposedSelect(relations *relationExecutor, sql string) (*queryexplanation.Document, error) {
	context := newComposedQueryContext(relations)
	local, body, err := parseAndMaterializeCTEs(context, sql, nil)
	if err != nil {
		return nil, err
	}
	root, err := s.explainComposedBody(local, body, nil)
	if err != nil {
		return nil, err
	}
	return queryexplanation.PlanOperator(s.server.config.Version, sql, s.database, root), nil
}

func (s *textStatementExecutor) explainComposedBody(context *composedQueryContext, query string, outer *outerRelationScope) (*queryexplanation.Operator, error) {
	query = stripWholeQueryParentheses(strings.TrimSpace(query))
	if local, body, err := parseAndMaterializeCTEs(context, query, outer); err != nil {
		return nil, err
	} else if body != query {
		return s.explainComposedBody(local, body, outer)
	}
	parsed, set, err := parseSetQuery(query)
	if err != nil {
		return nil, err
	}
	if set {
		return s.explainSetQuery(context, parsed, outer)
	}
	return s.explainSelectTerm(context, query, outer)
}

func (s *textStatementExecutor) explainSetQuery(context *composedQueryContext, query setQuery, outer *outerRelationScope) (*queryexplanation.Operator, error) {
	operators := make([]*queryexplanation.Operator, len(query.terms))
	for index, term := range query.terms {
		operator, err := s.explainComposedBody(context, term, outer)
		if err != nil {
			return nil, err
		}
		operators[index] = operator
	}
	root := reduceSetExplanation(operators, append([]setQueryOperation(nil), query.operations...))
	if strings.TrimSpace(query.order) != "" {
		orders := make([]queryexplanation.Order, 0)
		for _, item := range splitCSV(query.order) {
			expression, direction := splitOrderDirection(item)
			orders = append(orders, queryexplanation.Order{Expression: expression, Direction: direction})
		}
		root = queryexplanation.OrderedInput(root, orders)
	}
	limit, err := parseRelationalLimit(query.limit)
	if err != nil {
		return nil, err
	}
	if limit.present {
		root = queryexplanation.LimitedInput(root, queryexplanation.Limit{Present: true, Offset: limit.offset, Count: limit.count})
	}
	return root, nil
}

func reduceSetExplanation(operators []*queryexplanation.Operator, operations []setQueryOperation) *queryexplanation.Operator {
	operationCount := len(operations)
	for index := 0; index < operationCount; {
		if operations[index].kind != "intersect" {
			index++
			continue
		}
		operation := operations[index]
		operators[index] = queryexplanation.SetOperation(operation.kind, operation.all, operators[index], operators[index+1])
		operators = append(operators[:index+1], operators[index+2:]...)
		operations = append(operations[:index], operations[index+1:]...)
		operationCount--
	}
	root := operators[0]
	for index, operation := range operations {
		root = queryexplanation.SetOperation(operation.kind, operation.all, root, operators[index+1])
	}
	return root
}

func (s *textStatementExecutor) explainSelectTerm(context *composedQueryContext, query string, outer *outerRelationScope) (*queryexplanation.Operator, error) {
	lower := strings.ToLower(query)
	if !strings.HasPrefix(lower, "select ") {
		return nil, sqlFailure{1064, "42000", "composed query term must be SELECT"}
	}
	expression := strings.TrimSpace(query[len("SELECT "):])
	if keywordAt(expression, "from") < 0 {
		items := splitCSV(expression)
		return queryexplanation.PlanScalarSelect(s.server.config.Version, query, s.database, items).Plan, nil
	}
	executor := *context.executor
	executor.composed = context
	plan, err := parseRelationalSelectContext(&executor, query, outer)
	if err != nil {
		return nil, err
	}
	root := plan.explanation(s.server.config.Version, s.database, query).Plan
	return s.decorateComposedInputs(context, plan, root)
}

func (s *textStatementExecutor) decorateComposedInputs(context *composedQueryContext, plan *relationalSelectPlan, root *queryexplanation.Operator) (*queryexplanation.Operator, error) {
	for _, table := range plan.source.tables {
		if table.query == "" {
			continue
		}
		input, err := s.explainComposedBody(context, table.query, nil)
		if err != nil {
			return nil, err
		}
		root = queryexplanation.MaterializedInput(root, input, table.reason, "derived", table.alias)
	}
	outer := &outerRelationScope{columns: plan.source.columns, row: sampleRelationRow(plan.source.columns), parent: plan.outer}
	for _, projection := range plan.projection {
		if projection.subquery == "" {
			continue
		}
		input, err := s.explainComposedBody(context, projection.subquery, outer)
		if err != nil {
			return nil, err
		}
		root = queryexplanation.MaterializedInput(root, input, "subquery", "derived", projection.expression)
	}
	for _, query := range parenthesizedSelectQueries(plan.whereText) {
		input, err := s.explainComposedBody(context, query, outer)
		if err != nil {
			return nil, err
		}
		root = queryexplanation.MaterializedInput(root, input, "subquery", "where", "("+query+")")
	}
	return root, nil
}

func parenthesizedSelectQueries(value string) []string {
	queries := make([]string, 0)
	valueLength := len(value)
	for index := 0; index < valueLength; index++ {
		if value[index] == '\'' {
			index = skipQuoted(value, index)
			continue
		}
		if value[index] != '(' {
			continue
		}
		close, ok := matchingParenthesis(value, index)
		if !ok {
			break
		}
		candidate := strings.TrimSpace(value[index+1 : close])
		lower := strings.ToLower(candidate)
		if strings.HasPrefix(lower, "select ") || strings.HasPrefix(lower, "with ") {
			queries = append(queries, candidate)
		}
		index = close
	}
	return queries
}
