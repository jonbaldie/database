package mysql

import "strings"

func isSQLQuote(character byte) bool {
	return character == '\'' || character == '`' || character == '"'
}

func quotedSQLLiteralEnd(value string, start int) (int, bool) {
	length := len(value)
	if start < 0 || start >= length || !isSQLQuote(value[start]) {
		return 0, false
	}
	quote := value[start]
	for index := start + 1; index < length; index++ {
		if escapedSQLCharacter(value, index, quote) {
			index++
			continue
		}
		if value[index] != quote {
			continue
		}
		if doubledSQLQuote(value, index, quote) {
			index++
			continue
		}
		return index, true
	}
	return 0, false
}

func escapedSQLCharacter(value string, index int, quote byte) bool {
	if value[index] != '\\' || quote == '`' {
		return false
	}
	return index+1 < len(value)
}

func doubledSQLQuote(value string, index int, quote byte) bool {
	return index+1 < len(value) && value[index+1] == quote
}

func decodeMySQLStringValue(value string) (string, bool) {
	var builder strings.Builder
	valueLength := len(value)
	for index := 0; index < valueLength; index++ {
		switch value[index] {
		case '\\':
			if index+1 >= valueLength {
				return "", false
			}
			index++
			appendMySQLStringEscape(&builder, value[index])
		case '\'':
			if index+1 < len(value) && value[index+1] == '\'' {
				builder.WriteByte('\'')
				index++
				continue
			}
			builder.WriteByte('\'')
		default:
			builder.WriteByte(value[index])
		}
	}
	return builder.String(), true
}

func appendMySQLStringEscape(builder *strings.Builder, escaped byte) {
	if escaped == '%' || escaped == '_' {
		// MySQL keeps these escapes for LIKE pattern matching. The LIKE
		// evaluator consumes the backslash when it matches the pattern.
		builder.WriteByte('\\')
		builder.WriteByte(escaped)
		return
	}
	if decoded, ok := mySQLStringEscapeCharacters[escaped]; ok {
		builder.WriteByte(decoded)
		return
	}
	// MySQL ignores the backslash for all other escape sequences.
	builder.WriteByte(escaped)
}

var mySQLStringEscapeCharacters = map[byte]byte{
	'0':  0,
	'\'': '\'',
	'"':  '"',
	'b':  '\b',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'Z':  0x1a,
	'\\': '\\',
}

func scalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		if decoded, ok := decodeMySQLStringValue(value[1 : len(value)-1]); ok {
			return decoded
		}
	}
	return strings.Trim(value, "`")
}

func quote(value string) string {
	var builder strings.Builder
	builder.Grow(len(value) + 2)
	builder.WriteByte('\'')
	valueLength := len(value)
	for index := 0; index < valueLength; index++ {
		switch value[index] {
		case '\\':
			builder.WriteString("\\\\")
		case '\'':
			builder.WriteString("''")
		default:
			builder.WriteByte(value[index])
		}
	}
	builder.WriteByte('\'')
	return builder.String()
}
