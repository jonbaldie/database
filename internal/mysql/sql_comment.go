// This file removes MySQL comment text from a statement before policy
// dispatch. Line comments use `--` with whitespace or `#`, and block comments
// use `/* */`. Quoted strings and identifiers keep comment markers unchanged.
package mysql

import "strings"

func stripSQLComments(query string) string {
	var builder strings.Builder
	builder.Grow(len(query))
	length := len(query)
	for index := 0; index < length; {
		if next, ok := consumeSQLQuoted(query, index); ok {
			builder.WriteString(query[index:next])
			index = next
			continue
		}
		if next, ok := consumeSQLLineComment(query, index); ok {
			builder.WriteByte(' ')
			index = next
			continue
		}
		if next, ok := consumeSQLBlockComment(query, index); ok {
			builder.WriteByte(' ')
			index = next
			continue
		}
		builder.WriteByte(query[index])
		index++
	}
	return builder.String()
}

func consumeSQLQuoted(query string, index int) (int, bool) {
	if index >= len(query) || !isSQLQuote(query[index]) {
		return 0, false
	}
	if end, ok := quotedSQLLiteralEnd(query, index); ok {
		return end + 1, true
	}
	return len(query), true
}

func consumeSQLLineComment(query string, index int) (int, bool) {
	length := len(query)
	if index >= length {
		return 0, false
	}
	if query[index] == '#' {
		return skipSQLLineEnd(query, index+1), true
	}
	if index+1 < length && query[index] == '-' && query[index+1] == '-' && sqlCommentWhitespaceAt(query, index+2) {
		return skipSQLLineEnd(query, index+2), true
	}
	return 0, false
}

func consumeSQLBlockComment(query string, index int) (int, bool) {
	if index+1 >= len(query) || query[index] != '/' || query[index+1] != '*' {
		return 0, false
	}
	end := strings.Index(query[index+2:], "*/")
	if end < 0 {
		return 0, false
	}
	return index + 2 + end + 2, true
}

func sqlCommentWhitespaceAt(query string, index int) bool {
	return index < len(query) && isSQLCommentWhitespace(query[index])
}

func isSQLCommentWhitespace(character byte) bool {
	switch character {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

func skipSQLLineEnd(query string, index int) int {
	length := len(query)
	for index < length && query[index] != '\n' && query[index] != '\r' {
		index++
	}
	if index < length && query[index] == '\r' {
		index++
	}
	if index < length && query[index] == '\n' {
		index++
	}
	return index
}
