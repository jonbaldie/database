package mysql

import (
	"errors"
	"strconv"
	"strings"
)

// showClauses holds the optional tail of a catalog SHOW statement:
// {FROM|IN} name [{FROM|IN} name] [LIKE 'pattern' | WHERE expression].
type showClauses struct {
	from     []string
	like     string
	hasLike  bool
	where    string
	hasWhere bool
}

func showSyntaxFailure(text string) error {
	return sqlFailure{1064, "42000", "You have an error in your SQL syntax near '" + text + "'"}
}

// parseShowClauses splits the text that follows a SHOW noun into its FROM/IN,
// LIKE, and WHERE clauses. Quoted text is never split.
func parseShowClauses(rest string) (showClauses, error) {
	var clauses showClauses
	rest = strings.TrimSpace(rest)
	for rest != "" {
		word, remainder := nextShowWord(rest)
		tail, err := clauses.consume(strings.ToLower(word), remainder)
		if err != nil {
			return clauses, showSyntaxFailure(rest)
		}
		rest = strings.TrimSpace(tail)
	}
	return clauses, nil
}

// consume reads one clause introduced by keyword and returns the text after it.
func (c *showClauses) consume(keyword, remainder string) (string, error) {
	if c.hasLike || c.hasWhere {
		return "", errShowClause
	}
	switch keyword {
	case "from", "in":
		return c.consumeFrom(remainder)
	case "like":
		return c.consumeLike(remainder)
	case "where":
		return c.consumeWhere(remainder)
	}
	return "", errShowClause
}

func (c *showClauses) consumeFrom(remainder string) (string, error) {
	name, tail, ok := nextShowIdentifier(remainder)
	if !ok || len(c.from) == 2 {
		return "", errShowClause
	}
	c.from = append(c.from, name)
	return tail, nil
}

func (c *showClauses) consumeLike(remainder string) (string, error) {
	literal, tail, ok := nextShowString(remainder)
	if !ok {
		return "", errShowClause
	}
	c.like, c.hasLike = literal, true
	return tail, nil
}

func (c *showClauses) consumeWhere(remainder string) (string, error) {
	if strings.TrimSpace(remainder) == "" {
		return "", errShowClause
	}
	c.where, c.hasWhere = strings.TrimSpace(remainder), true
	return "", nil
}

var errShowClause = errors.New("invalid SHOW clause")

func nextShowWord(text string) (string, string) {
	text = strings.TrimSpace(text)
	end := strings.IndexAny(text, " \t\r\n")
	if end < 0 {
		return text, ""
	}
	return text[:end], text[end:]
}

// nextShowIdentifier reads one possibly qualified, possibly backquoted name.
func nextShowIdentifier(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	inQuote := false
	size := len(text)
	for index := 0; index < size; index++ {
		switch character := text[index]; {
		case character == '`':
			inQuote = !inQuote
		case !inQuote && strings.IndexByte(" \t\r\n", character) >= 0:
			return text[:index], text[index:], index > 0
		}
	}
	return text, "", text != "" && !inQuote
}

// nextShowString reads one quoted string literal and returns its decoded value.
func nextShowString(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || !isSQLStringQuote(text[0]) {
		return "", "", false
	}
	end := closingQuoteIndex(text)
	if end < 0 {
		return "", "", false
	}
	return scalar(text[:end+1]), text[end+1:], true
}

// closingQuoteIndex finds the quote that closes the literal opened at text[0],
// skipping doubled quotes and backslash escapes.
func closingQuoteIndex(text string) int {
	quoteByte := text[0]
	size := len(text)
	for index := 1; index < size; index++ {
		switch {
		case text[index] == '\\' && quoteByte != '`':
			index++
		case text[index] != quoteByte:
		case index+1 < size && text[index+1] == quoteByte:
			index++
		default:
			return index
		}
	}
	return -1
}

// filterShowRows keeps the rows of a SHOW result for which keep is true, so
// rows and their NULL flags stay aligned.
func filterShowRows(result *queryResult, keep func(row []string, null []bool) (bool, error)) (*queryResult, error) {
	rows := make([][]string, 0, len(result.rows))
	var nulls [][]bool
	for index, row := range result.rows {
		var null []bool
		if index < len(result.nulls) {
			null = result.nulls[index]
		}
		kept, err := keep(row, null)
		if err != nil {
			return nil, err
		}
		if !kept {
			continue
		}
		rows = append(rows, row)
		if result.nulls != nil {
			nulls = append(nulls, null)
		}
	}
	result.rows = rows
	if result.nulls != nil {
		result.nulls = nulls
	}
	return result, nil
}

// applyShowClauses filters a SHOW result by its LIKE pattern, which matches
// the first column, or by its WHERE expression over the result columns.
// numeric names the columns that MySQL types as integers.
func (s *catalogExecutor) applyShowClauses(result *queryResult, clauses showClauses, numeric ...string) (*queryResult, error) {
	switch {
	case clauses.hasLike:
		return filterShowRows(result, func(row []string, _ []bool) (bool, error) {
			return len(row) > 0 && mysqlLike(row[0], clauses.like), nil
		})
	case clauses.hasWhere:
		return filterShowRows(result, func(row []string, null []bool) (bool, error) {
			return s.showWhereMatches(result.columns, row, null, clauses.where, numeric)
		})
	}
	return result, nil
}

func (s *catalogExecutor) showWhereMatches(columns, row []string, null []bool, where string, numeric []string) (bool, error) {
	value, err := evaluateScalarResolved(where, func(name string) (exprValue, error) {
		return showColumnValue(columns, row, null, strings.Trim(name, "`"), numeric)
	}, s.session)
	if err != nil {
		return false, err
	}
	known, truth, err := truthValue(value)
	return known && truth, err
}

// showColumnValue resolves one column of a SHOW row for a WHERE expression.
func showColumnValue(columns, row []string, null []bool, name string, numeric []string) (exprValue, error) {
	for index, column := range columns {
		if !strings.EqualFold(column, name) || index >= len(row) {
			continue
		}
		if index < len(null) && null[index] {
			return nullValue(), nil
		}
		if number, err := strconv.ParseInt(row[index], 10, 64); err == nil && containsFold(numeric, column) {
			return intValue(number), nil
		}
		return stringValue(row[index]), nil
	}
	return exprValue{}, sqlFailure{1054, "42S22", "Unknown column '" + name + "' in 'where clause'"}
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

// showCharacterSet and showCollation report the closed character-set surface.
func showCharacterSet() *queryResult {
	return &queryResult{
		columns: []string{"Charset", "Description", "Default collation", "Maxlen"},
		rows:    [][]string{{"utf8mb4", "UTF-8 Unicode", "utf8mb4_0900_ai_ci", "4"}},
	}
}

func showCollation() *queryResult {
	return &queryResult{
		columns: []string{"Collation", "Charset", "Id", "Default", "Compiled", "Sortlen", "Pad_attribute"},
		rows: [][]string{
			{"utf8mb4_0900_ai_ci", "utf8mb4", "255", "Yes", "Yes", "0", "NO PAD"},
			{"utf8mb4_bin", "utf8mb4", "46", "", "Yes", "1", "PAD SPACE"},
		},
	}
}

// filterShowLike keeps the rows whose first column matches a LIKE pattern.
func filterShowLike(result *queryResult, pattern string) *queryResult {
	filtered, _ := filterShowRows(result, func(row []string, _ []bool) (bool, error) {
		return len(row) > 0 && mysqlLike(row[0], pattern), nil
	})
	return filtered
}
