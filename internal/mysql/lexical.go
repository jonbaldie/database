package mysql

import "strings"

func identifier(value string) string {
	name, ok := singleIdentifier(value)
	if !ok {
		return ""
	}
	return name
}

func singleIdentifier(value string) (string, bool) {
	parts, ok := splitQualifiedIdentifier(value)
	return firstIdentifierPart(parts, ok)
}

func firstIdentifierPart(parts []string, valid bool) (string, bool) {
	if !valid || len(parts) != 1 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

// splitQualifiedIdentifier parses MySQL-style backtick escaping while keeping
// dots inside quoted identifiers. It accepts only a complete identifier list,
// so trailing SQL cannot be mistaken for a name.
func splitQualifiedIdentifier(value string) ([]string, bool) {
	parser := qualifiedIdentifierParser{remaining: strings.TrimSpace(value)}
	return parser.parse()
}

type qualifiedIdentifierParser struct {
	remaining string
	parts     []string
}

func (p *qualifiedIdentifierParser) parse() ([]string, bool) {
	if p.remaining == "" {
		return nil, false
	}
	for p.hasRemaining() {
		p.remaining = strings.TrimSpace(p.remaining)
		if p.remaining == "" {
			return nil, false
		}
		quoted, ok := p.consumePart()
		if !ok {
			return nil, false
		}
		if p.remaining == "" {
			return p.parts, true
		}
		if !quoted {
			continue
		}
		if !p.consumeQuotedSeparator() {
			return nil, false
		}
	}
	return p.parts, len(p.parts) > 0
}

func (p *qualifiedIdentifierParser) hasRemaining() bool { return len(p.remaining) > 0 }

func (p *qualifiedIdentifierParser) consumePart() (bool, bool) {
	quoted := p.remaining[0] == '`'
	name, remainder, ok := parseIdentifierPart(p.remaining)
	if !ok {
		return false, false
	}
	p.parts = append(p.parts, name)
	p.remaining = remainder
	return quoted, true
}

func (p *qualifiedIdentifierParser) consumeQuotedSeparator() bool {
	p.remaining = strings.TrimSpace(p.remaining)
	if p.remaining == "" {
		return true
	}
	if p.remaining[0] != '.' {
		return false
	}
	p.remaining = p.remaining[1:]
	return true
}

func parseIdentifierPart(value string) (string, string, bool) {
	if value[0] == '`' {
		return parseQuotedIdentifierPart(value)
	}
	return parseBareIdentifierPart(value)
}

func parseQuotedIdentifierPart(value string) (string, string, bool) {
	var name strings.Builder
	valueLength := len(value)
	for index := 1; index < valueLength; index++ {
		if value[index] != '`' {
			name.WriteByte(value[index])
			continue
		}
		if hasEscapedBacktick(value, index) {
			name.WriteByte('`')
			index++
			continue
		}
		return name.String(), value[index+1:], true
	}
	return "", "", false
}

func hasEscapedBacktick(value string, index int) bool {
	return index+1 < len(value) && value[index+1] == '`'
}

func parseBareIdentifierPart(value string) (string, string, bool) {
	end := strings.IndexByte(value, '.')
	if end < 0 {
		name := strings.TrimSpace(value)
		return name, "", name != ""
	}
	name := strings.TrimSpace(value[:end])
	if name == "" || strings.ContainsAny(name, " \t\r\n`") {
		return "", "", false
	}
	return name, value[end+1:], true
}

func consumeIdentifier(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	token, remainder, ok := leadingIdentifierToken(value)
	if !ok {
		return "", "", false
	}
	name, ok := singleIdentifier(token)
	return name, remainder, ok
}

func leadingIdentifierToken(value string) (string, string, bool) {
	if value[0] == '`' {
		return quotedIdentifierToken(value)
	}
	end := strings.IndexAny(value, " \t\r\n")
	if end < 0 {
		return value, "", true
	}
	return value[:end], value[end:], true
}

func quotedIdentifierToken(value string) (string, string, bool) {
	valueLength := len(value)
	for index := 1; index < valueLength; index++ {
		if value[index] != '`' {
			continue
		}
		if hasEscapedBacktick(value, index) {
			index++
			continue
		}
		return value[:index+1], value[index+1:], true
	}
	return "", "", false
}

func parameterCount(query string) int { return len(preparedPlaceholders(query)) }

func countPreparedParameters(query string, maximum int) (int, bool) {
	positions, withinLimit := scanPreparedPlaceholders(query, maximum)
	return len(positions), withinLimit
}

func preparedPlaceholders(query string) []int {
	positions, _ := scanPreparedPlaceholders(query, -1)
	return positions
}

func scanPreparedPlaceholders(query string, maximum int) ([]int, bool) {
	scanner := placeholderScanner{query: query}
	return scanner.scan(maximum)
}

type placeholderScanner struct {
	query     string
	quote     byte
	escaped   bool
	positions []int
}

func (s *placeholderScanner) scan(maximum int) ([]int, bool) {
	queryLength := len(s.query)
	for index := 0; index < queryLength; index++ {
		character := s.query[index]
		if s.inQuote() {
			index = s.consumeQuoted(index, character)
			continue
		}
		if startsQuote(character) {
			s.quote = character
			continue
		}
		if character == '?' && !s.addPlaceholder(index, maximum) {
			return s.positions, false
		}
	}
	return s.positions, true
}

func (s *placeholderScanner) inQuote() bool { return s.quote != 0 }

func (s *placeholderScanner) consumeQuoted(index int, character byte) int {
	if s.escaped {
		s.escaped = false
		return index
	}
	if character == '\\' {
		s.escaped = true
		return index
	}
	if character == s.quote {
		return s.closeQuote(index)
	}
	return index
}

func (s *placeholderScanner) closeQuote(index int) int {
	if s.quote == '\'' && hasEscapedQuote(s.query, index) {
		return index + 1
	}
	s.quote = 0
	return index
}

func hasEscapedQuote(value string, index int) bool {
	return index+1 < len(value) && value[index+1] == '\''
}

func startsQuote(character byte) bool {
	return character == '\'' || character == '"' || character == '`'
}

func (s *placeholderScanner) addPlaceholder(index, maximum int) bool {
	s.positions = append(s.positions, index)
	return maximum < 0 || len(s.positions) <= maximum
}
