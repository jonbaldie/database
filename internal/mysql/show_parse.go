package mysql

import "strings"

type showModifiers struct {
	objectParts []string
	namespace   string
	like        string
	hasLike     bool
	where       string
}

func parseShowModifiers(text string, requireObject bool) (showModifiers, error) {
	modifiers, rest, err := parseShowObject(strings.TrimSpace(text), requireObject)
	if err != nil {
		return modifiers, err
	}
	modifiers, rest, err = parseShowFromIn(modifiers, rest)
	if err != nil {
		return modifiers, err
	}
	return parseShowFilter(modifiers, rest)
}

func parseShowObject(text string, requireObject bool) (showModifiers, string, error) {
	var modifiers showModifiers
	if !requireObject {
		return modifiers, text, nil
	}
	parts, rest, ok := consumeQualifiedIdentifier(text)
	if !ok || len(parts) == 0 || len(parts) > 2 {
		return modifiers, rest, sqlFailure{1064, "42000", "invalid table name"}
	}
	modifiers.objectParts = parts
	return modifiers, rest, nil
}

func parseShowFromIn(modifiers showModifiers, text string) (showModifiers, string, error) {
	text = strings.TrimSpace(text)
	if !hasKeywordPrefix(strings.ToLower(text), "from") && !hasKeywordPrefix(strings.ToLower(text), "in") {
		return modifiers, text, nil
	}
	namespace, rest, err := consumeShowNamespace(text)
	if err != nil {
		return modifiers, rest, err
	}
	modifiers.namespace = namespace
	return modifiers, rest, nil
}

func parseShowFilter(modifiers showModifiers, text string) (showModifiers, error) {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	switch {
	case hasKeywordPrefix(lower, "like"):
		modifiers.like = scalar(skipKeyword(text, "like"))
		modifiers.hasLike = true
		return modifiers, nil
	case hasKeywordPrefix(lower, "where"):
		return parseShowWhereFilter(modifiers, text)
	case text != "":
		return modifiers, sqlFailure{1064, "42000", "unsupported query"}
	default:
		return modifiers, nil
	}
}

func parseShowWhereFilter(modifiers showModifiers, text string) (showModifiers, error) {
	modifiers.where = strings.TrimSpace(skipKeyword(text, "where"))
	if modifiers.where == "" {
		return modifiers, sqlFailure{1064, "42000", "unsupported WHERE clause"}
	}
	return modifiers, nil
}

func consumeShowNamespace(text string) (string, string, error) {
	text = skipKeyword(text, keywordPrefix(text, "from", "in"))
	name, rest, ok := consumeIdentifier(text)
	if !ok || name == "" {
		return "", "", sqlFailure{1064, "42000", "invalid database name"}
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, ".") {
		return "", "", sqlFailure{1064, "42000", "invalid database name"}
	}
	return name, rest, nil
}

func consumeQualifiedIdentifier(value string) ([]string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, value, false
	}
	parts := make([]string, 0, 2)
	for {
		name, rest, ok := consumeShowIdentifierPart(value)
		if !ok || name == "" {
			return nil, value, false
		}
		parts = append(parts, name)
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, ".") {
			value = strings.TrimSpace(rest[1:])
			continue
		}
		return parts, rest, true
	}
}

func consumeShowIdentifierPart(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if value[0] == '`' {
		return parseQuotedIdentifierPart(value)
	}
	end := showIdentifierEnd(value)
	if end == 0 {
		return "", value, false
	}
	return value[:end], value[end:], true
}

func showIdentifierEnd(value string) int {
	end := len(value)
	valueLength := len(value)
	for index := 0; index < valueLength; index++ {
		if value[index] == '.' || value[index] == '(' || isSQLSpace(value[index]) {
			return index
		}
	}
	return end
}

func cutShowPrefix(query, lower, prefix string) (string, bool) {
	query = strings.TrimSpace(query)
	lower = strings.TrimSpace(lower)
	if lower == prefix {
		return "", true
	}
	if strings.HasPrefix(lower, prefix) && len(lower) > len(prefix) && isSQLSpace(lower[len(prefix)]) {
		return strings.TrimSpace(query[len(prefix):]), true
	}
	return "", false
}

func isShowPrefix(lower, prefix string) bool {
	_, ok := cutShowPrefix(lower, lower, prefix)
	return ok
}

func hasKeywordPrefix(lower, keyword string) bool {
	if !strings.HasPrefix(lower, keyword) {
		return false
	}
	if len(lower) == len(keyword) {
		return true
	}
	return isSQLSpace(lower[len(keyword)])
}

func skipKeyword(text, keyword string) string {
	text = strings.TrimSpace(text)
	if len(text) < len(keyword) {
		return text
	}
	return strings.TrimSpace(text[len(keyword):])
}

func keywordPrefix(text string, keywords ...string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, keyword := range keywords {
		if hasKeywordPrefix(lower, keyword) {
			return keyword
		}
	}
	return ""
}

func isSQLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func applyShowFilters(session *session, result *queryResult, modifiers showModifiers) (*queryResult, error) {
	if modifiers.hasLike {
		result = filterShowLikePattern(result, modifiers.like)
	}
	return filterShowWhere(session, result, modifiers.where)
}

func filterShowLikePattern(result *queryResult, pattern string) *queryResult {
	if result == nil {
		return result
	}
	kept := make([]int, 0, len(result.rows))
	for index, row := range result.rows {
		if len(row) > 0 && mysqlLike(row[0], pattern) {
			kept = append(kept, index)
		}
	}
	return keepShowRows(result, kept)
}

func filterShowWhere(session *session, result *queryResult, where string) (*queryResult, error) {
	if result == nil || strings.TrimSpace(where) == "" {
		return result, nil
	}
	predicate, err := compileShowWhere(session, result.columns, where)
	if err != nil {
		return nil, err
	}
	kept, err := matchingShowRows(result.rows, predicate)
	if err != nil {
		return nil, err
	}
	return keepShowRows(result, kept), nil
}

func compileShowWhere(session *session, names []string, where string) (relationPredicate, error) {
	columns := make([]relationColumn, len(names))
	for index, name := range names {
		columns[index] = relationColumn{name: name, typeName: "VARCHAR(256)", index: index}
	}
	return compileRelationPredicate(where, columns, session)
}

func matchingShowRows(rows [][]string, predicate relationPredicate) ([]int, error) {
	kept := make([]int, 0, len(rows))
	for index, row := range rows {
		matched, err := predicateMatches(predicate, relationRow{values: row})
		if err != nil {
			return nil, err
		}
		if matched {
			kept = append(kept, index)
		}
	}
	return kept, nil
}

func keepShowRows(result *queryResult, kept []int) *queryResult {
	rows := make([][]string, 0, len(kept))
	for _, index := range kept {
		rows = append(rows, result.rows[index])
	}
	result.rows = rows
	if len(result.nulls) == 0 {
		return result
	}
	nulls := make([][]bool, 0, len(kept))
	for _, index := range kept {
		if index < len(result.nulls) {
			nulls = append(nulls, result.nulls[index])
		}
	}
	result.nulls = nulls
	return result
}
