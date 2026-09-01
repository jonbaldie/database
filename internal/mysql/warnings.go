package mysql

import (
	"errors"
	"strconv"
	"strings"
)

func (s *session) clearDiagnostics() {
	s.diagnostics = nil
}

func (s *session) replaceDiagnostics(diagnostics []sqlDiagnostic) {
	s.diagnostics = append([]sqlDiagnostic(nil), diagnostics...)
}

func (s *session) addDiagnostic(level string, code uint16, message string) {
	s.diagnostics = append(s.diagnostics, sqlDiagnostic{level: level, code: code, message: message})
}

func (s *session) diagnosticCount() uint16 {
	if len(s.diagnostics) > int(^uint16(0)) {
		return ^uint16(0)
	}
	return uint16(len(s.diagnostics))
}

func diagnosticForError(err error) sqlDiagnostic {
	var failure sqlFailure
	if errors.As(err, &failure) {
		return sqlDiagnostic{level: "Error", code: failure.code, message: failure.message}
	}
	return sqlDiagnostic{level: "Error", code: 1064, message: err.Error()}
}

func isShowDiagnosticsStatement(lower string) bool {
	fields := strings.Fields(strings.ToLower(lower))
	return len(fields) >= 2 && fields[0] == "show" && (fields[1] == "warnings" || fields[1] == "errors")
}

func (s *textStatementExecutor) showDiagnostics(query, lower string) (*queryResult, bool, error) {
	errorsOnly, limitText, hasLimit, handled := showDiagnosticsParts(query, lower)
	if !handled {
		return nil, false, nil
	}
	limit := relationalLimit{}
	var err error
	if hasLimit && limitText == "" {
		return nil, true, sqlFailure{1064, "42000", "SHOW WARNINGS requires a LIMIT value"}
	}
	if hasLimit {
		limit, err = parseRelationalLimit(limitText)
		if err != nil {
			return nil, true, err
		}
	}
	rows := make([][]string, 0, len(s.diagnostics))
	for _, diagnostic := range s.diagnostics {
		if errorsOnly && !strings.EqualFold(diagnostic.level, "Error") {
			continue
		}
		rows = append(rows, []string{diagnostic.level, strconv.Itoa(int(diagnostic.code)), diagnostic.message})
	}
	rows = applyDiagnosticLimit(rows, limit)
	return &queryResult{
		columns:  []string{"Level", "Code", "Message"},
		rows:     rows,
		metadata: diagnosticMetadata(),
	}, true, nil
}

func showDiagnosticsParts(query, lower string) (errorsOnly bool, limitText string, hasLimit bool, handled bool) {
	lower = strings.TrimSpace(strings.ToLower(lower))
	query = strings.TrimSpace(query)
	fields := strings.Fields(lower)
	if len(fields) < 2 || fields[0] != "show" || (fields[1] != "warnings" && fields[1] != "errors") {
		return false, "", false, false
	}
	errorsOnly = fields[1] == "errors"
	prefixWord := "warnings"
	if errorsOnly {
		prefixWord = "errors"
	}
	prefixLength := showDiagnosticsPrefixLength(query, prefixWord)
	if prefixLength < 0 {
		return errorsOnly, "", true, true
	}
	suffix := strings.TrimSpace(query[prefixLength:])
	if suffix == "" {
		return errorsOnly, "", false, true
	}
	limitEnd := consumeSQLWord(suffix, "limit")
	if limitEnd < 0 {
		return errorsOnly, "", true, true
	}
	return errorsOnly, strings.TrimSpace(suffix[limitEnd:]), true, true
}

func showDiagnosticsPrefixLength(query, secondWord string) int {
	position := 0
	var ok bool
	if position, ok = consumeDiagnosticsPrefixWord(query, position, "show"); !ok {
		return -1
	}
	if position, ok = consumeDiagnosticsPrefixWord(query, position, secondWord); !ok {
		return -1
	}
	if position < len(query) && isSQLWord(rune(query[position])) {
		return -1
	}
	return position
}

func consumeDiagnosticsPrefixWord(query string, position int, word string) (int, bool) {
	if position > 0 {
		position = skipDiagnosticsWhitespace(query, position)
	}
	start := position
	queryLength := len(query)
	for position < queryLength && isSQLWord(rune(query[position])) {
		position++
	}
	return position, start < position && strings.EqualFold(query[start:position], word)
}

func skipDiagnosticsWhitespace(query string, position int) int {
	queryLength := len(query)
	for position < queryLength {
		switch query[position] {
		case ' ', '\t', '\n', '\r':
			position++
		default:
			return position
		}
	}
	return position
}

func consumeSQLWord(value, word string) int {
	if len(value) < len(word) || !strings.EqualFold(value[:len(word)], word) {
		return -1
	}
	if len(value) > len(word) && isSQLWord(rune(value[len(word)])) {
		return -1
	}
	return len(word)
}

func applyDiagnosticLimit(rows [][]string, limit relationalLimit) [][]string {
	if !limit.present {
		return rows
	}
	if limit.offset >= len(rows) || limit.count == 0 {
		return nil
	}
	end := limit.offset + limit.count
	if end < limit.offset || end > len(rows) {
		end = len(rows)
	}
	return rows[limit.offset:end]
}

func diagnosticMetadata() []columnMetadata {
	return []columnMetadata{
		{catalog: "def", name: "Level", characterSet: mysqlCharsetUTF8MB40900AICI, length: 7, typ: mysqlTypeVarString},
		{catalog: "def", name: "Code", characterSet: mysqlCharsetBinary, length: 10, typ: mysqlTypeLong, flags: mysqlUnsignedFlag},
		{catalog: "def", name: "Message", characterSet: mysqlCharsetUTF8MB40900AICI, length: 1024, typ: mysqlTypeVarString},
	}
}
