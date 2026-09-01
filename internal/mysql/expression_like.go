package mysql

import (
	"strings"
	"unicode/utf8"
)

func evalLike(value, pattern exprValue, escape string, negate bool) (exprValue, error) {
	if value.isNull() || pattern.isNull() {
		return nullValue(), nil
	}
	if err := requireCharacterLikeOperand(value); err != nil {
		return exprValue{}, err
	}
	if err := requireCharacterLikeOperand(pattern); err != nil {
		return exprValue{}, err
	}
	escapeRune, ok := likeEscapeRune(escape)
	if !ok {
		return exprValue{}, unsupportedExpression()
	}
	matched := likeMatch(value.render(), pattern.render(), escapeRune, value.collation != collationBin)
	return boolValue(matched != negate), nil
}

func requireCharacterLikeOperand(value exprValue) error {
	if value.kind == valueString {
		return nil
	}
	return strictConversionError()
}

func likeEscapeRune(escape string) (rune, bool) {
	if escape == "" {
		return '\\', true
	}
	r, size := utf8.DecodeRuneInString(escape)
	if r == utf8.RuneError || size != len(escape) {
		return 0, false
	}
	return r, true
}

func likeMatch(value, pattern string, escape rune, fold bool) bool {
	return likeMatchRunes([]rune(value), []rune(pattern), escape, fold)
}

type likeTokenKind byte

const (
	likeLiteral likeTokenKind = iota
	likeSingle
	likeAny
)

type likeToken struct {
	kind    likeTokenKind
	literal rune
}

func likeMatchRunes(value, pattern []rune, escape rune, fold bool) bool {
	// Dynamic programming keeps one bounded row for each pattern token. A
	// percent token consumes zero or more value runes, so its row recurrence is
	// previous[i] (zero runes) or current[i-1] (one more rune). This removes the
	// recursive backtracking that made multiple percent wildcards exponential.
	tokens := tokenizeLikePattern(pattern, escape)

	valueLength := len(value)
	previous := make([]bool, valueLength+1)
	current := make([]bool, valueLength+1)
	previous[0] = true
	for _, token := range tokens {
		clear(current)
		applyLikeToken(token, value, valueLength, previous, current, fold)
		previous, current = current, previous
	}
	return previous[valueLength]
}

func tokenizeLikePattern(pattern []rune, escape rune) []likeToken {
	tokens := make([]likeToken, 0, len(pattern))
	patternLength := len(pattern)
	for index := 0; index < patternLength; index++ {
		if pattern[index] == escape && index+1 < patternLength {
			tokens = append(tokens, likeToken{kind: likeLiteral, literal: pattern[index+1]})
			index++
			continue
		}
		switch pattern[index] {
		case '%':
			tokens = append(tokens, likeToken{kind: likeAny})
		case '_':
			tokens = append(tokens, likeToken{kind: likeSingle})
		default:
			tokens = append(tokens, likeToken{kind: likeLiteral, literal: pattern[index]})
		}
	}
	return tokens
}

func applyLikeToken(token likeToken, value []rune, valueLength int, previous, current []bool, fold bool) {
	switch token.kind {
	case likeLiteral:
		fillLikeLiteralRow(token.literal, value, valueLength, previous, current, fold)
	case likeSingle:
		fillLikeSingleRow(valueLength, previous, current)
	case likeAny:
		fillLikeAnyRow(valueLength, previous, current)
	}
}

func fillLikeLiteralRow(want rune, value []rune, valueLength int, previous, current []bool, fold bool) {
	for valueIndex := 1; valueIndex <= valueLength; valueIndex++ {
		current[valueIndex] = previous[valueIndex-1] && likeRuneEqual(value[valueIndex-1], want, fold)
	}
}

func fillLikeSingleRow(valueLength int, previous, current []bool) {
	for valueIndex := 1; valueIndex <= valueLength; valueIndex++ {
		current[valueIndex] = previous[valueIndex-1]
	}
}

func fillLikeAnyRow(valueLength int, previous, current []bool) {
	current[0] = previous[0]
	for valueIndex := 1; valueIndex <= valueLength; valueIndex++ {
		current[valueIndex] = previous[valueIndex] || current[valueIndex-1]
	}
}

func likeRuneEqual(left, right rune, fold bool) bool {
	if left == right || !fold {
		return left == right
	}
	return characterComparisonKey(defaultStringType, string(left)) == characterComparisonKey(defaultStringType, string(right))
}

func mysqlLike(value, pattern string) bool {
	return likeMatch(strings.ToLower(value), strings.ToLower(pattern), '\\', false)
}
