// This file tokenizes a scalar expression into the finite token vocabulary the
// parser consumes: numbers, single-quoted strings, identifiers and keywords,
// operators, and the grouping punctuation. A character the grammar does not
// define ends tokenizing with an unsupported-expression error before any value
// is produced.
package mysql

import "strings"

type exprTokenKind int

const (
	tokenNumber exprTokenKind = iota
	tokenString
	tokenIdent
	tokenOperator
	tokenLParen
	tokenRParen
	tokenComma
)

// exprToken is one lexical unit. text holds the raw spelling for a number, the
// identifier, or the normalized operator symbol; str holds the decoded content
// of a string literal.
type exprToken struct {
	kind exprTokenKind
	text string
	str  string
}

// multiCharOperators lists the operator spellings longer than one byte, ordered
// longest first so <=> is preferred over <= and <.
var multiCharOperators = []string{"<=>", "<=", ">=", "<>", "!="}

const singleCharOperators = "=<>+-*/%"

func tokenizeExpression(input string) ([]exprToken, error) {
	tokens := make([]exprToken, 0, len(input)/2+1)
	cursor, length := 0, len(input)
	for cursor < length {
		if isExpressionSpace(input[cursor]) {
			cursor++
			continue
		}
		token, next, err := scanToken(input, cursor)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
		cursor = next
	}
	return tokens, nil
}

func isExpressionSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\n' || character == '\r'
}

func scanToken(input string, cursor int) (exprToken, int, error) {
	character := input[cursor]
	switch {
	case character == '\'':
		return scanString(input, cursor)
	case startsNumber(input, cursor):
		return scanNumber(input, cursor)
	case isIdentifierStart(character):
		token, next := scanIdentifier(input, cursor)
		return token, next, nil
	default:
		return scanPunctuationOrOperator(input, cursor, character)
	}
}

func scanPunctuationOrOperator(input string, cursor int, character byte) (exprToken, int, error) {
	switch character {
	case '(':
		return exprToken{kind: tokenLParen}, cursor + 1, nil
	case ')':
		return exprToken{kind: tokenRParen}, cursor + 1, nil
	case ',':
		return exprToken{kind: tokenComma}, cursor + 1, nil
	default:
		return scanOperator(input, cursor)
	}
}

// startsNumber reports whether the cursor begins a numeric run: a digit, or a
// decimal point immediately followed by a digit.
func startsNumber(input string, cursor int) bool {
	if isDigit(input[cursor]) {
		return true
	}
	return input[cursor] == '.' && cursor+1 < len(input) && isDigit(input[cursor+1])
}

// scanString reads a single-quoted string, decoding a doubled quote into one
// literal quote. An unterminated string is unsupported.
func scanString(input string, cursor int) (exprToken, int, error) {
	var builder strings.Builder
	index, length := cursor+1, len(input)
	for index < length {
		if input[index] != '\'' {
			builder.WriteByte(input[index])
			index++
			continue
		}
		if index+1 < length && input[index+1] == '\'' {
			builder.WriteByte('\'')
			index += 2
			continue
		}
		return exprToken{kind: tokenString, str: builder.String()}, index + 1, nil
	}
	return exprToken{}, 0, unsupportedExpression()
}

// scanNumber reads a numeric run: an integer part, an optional fractional part,
// and an optional decimal exponent. The raw spelling is preserved so evaluation
// can decide the exact integer, decimal, or approximate domain.
func scanNumber(input string, cursor int) (exprToken, int, error) {
	index := consumeDigits(input, cursor)
	if index < len(input) && input[index] == '.' {
		index = consumeDigits(input, index+1)
	}
	if index < len(input) && (input[index] == 'e' || input[index] == 'E') {
		exponentEnd, ok := consumeExponent(input, index)
		if !ok {
			return exprToken{}, 0, unsupportedExpression()
		}
		index = exponentEnd
	}
	return exprToken{kind: tokenNumber, text: input[cursor:index]}, index, nil
}

func consumeExponent(input string, index int) (int, bool) {
	next := index + 1
	if next < len(input) && (input[next] == '+' || input[next] == '-') {
		next++
	}
	digitsEnd := consumeDigits(input, next)
	if digitsEnd == next {
		return 0, false
	}
	return digitsEnd, true
}

func consumeDigits(input string, index int) int {
	length := len(input)
	for index < length && isDigit(input[index]) {
		index++
	}
	return index
}

func scanIdentifier(input string, cursor int) (exprToken, int) {
	index, length := consumeIdentifierPart(input, cursor), len(input)
	for index+1 < length && input[index] == '.' && isIdentifierStart(input[index+1]) {
		index = consumeIdentifierPart(input, index+1)
	}
	return exprToken{kind: tokenIdent, text: input[cursor:index]}, index
}

func consumeIdentifierPart(input string, cursor int) int {
	index, length := cursor+1, len(input)
	for index < length && isIdentifierPart(input[index]) {
		index++
	}
	return index
}

// scanOperator reads the longest matching operator spelling. A byte that starts
// no operator is outside the grammar and fails.
func scanOperator(input string, cursor int) (exprToken, int, error) {
	for _, candidate := range multiCharOperators {
		if strings.HasPrefix(input[cursor:], candidate) {
			return exprToken{kind: tokenOperator, text: candidate}, cursor + len(candidate), nil
		}
	}
	if strings.IndexByte(singleCharOperators, input[cursor]) >= 0 {
		return exprToken{kind: tokenOperator, text: input[cursor : cursor+1]}, cursor + 1, nil
	}
	return exprToken{}, 0, unsupportedExpression()
}

func isDigit(character byte) bool { return character >= '0' && character <= '9' }

func isIdentifierStart(character byte) bool {
	return character == '_' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func isIdentifierPart(character byte) bool {
	return isIdentifierStart(character) || isDigit(character)
}
