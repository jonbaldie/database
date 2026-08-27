package mysql

import (
	"strings"
	"unicode"
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

func likeMatchRunes(value, pattern []rune, escape rune, fold bool) bool {
	valueIndex, patternIndex := 0, 0
	valueLength, patternLength := len(value), len(pattern)
	for patternIndex < patternLength {
		consumed, ok := consumeLikeToken(value, pattern, &valueIndex, &patternIndex, valueLength, patternLength, escape, fold)
		if !ok {
			return false
		}
		if consumed {
			return true
		}
	}
	return valueIndex == valueLength
}

func consumeLikeToken(value, pattern []rune, valueIndex, patternIndex *int, valueLength, patternLength int, escape rune, fold bool) (bool, bool) {
	if pattern[*patternIndex] == escape && *patternIndex+1 < patternLength {
		return false, consumeLikeLiteral(value, pattern[*patternIndex+1], valueIndex, patternIndex, valueLength, 2, fold)
	}
	switch pattern[*patternIndex] {
	case '%':
		*patternIndex++
		return likeMatchPercent(value, pattern, *valueIndex, *patternIndex, escape, fold), true
	case '_':
		if *valueIndex == valueLength {
			return false, false
		}
		*valueIndex++
		*patternIndex++
		return false, true
	default:
		return false, consumeLikeLiteral(value, pattern[*patternIndex], valueIndex, patternIndex, valueLength, 1, fold)
	}
}

func consumeLikeLiteral(value []rune, want rune, valueIndex, patternIndex *int, valueLength, patternAdvance int, fold bool) bool {
	if *valueIndex == valueLength || !likeRuneEqual(value[*valueIndex], want, fold) {
		return false
	}
	*valueIndex++
	*patternIndex += patternAdvance
	return true
}

func likeMatchPercent(value, pattern []rune, valueIndex, patternIndex int, escape rune, fold bool) bool {
	if patternIndex == len(pattern) {
		return true
	}
	limit := len(value)
	for index := valueIndex; index <= limit; index++ {
		if likeMatchRunes(value[index:], pattern[patternIndex:], escape, fold) {
			return true
		}
	}
	return false
}

func likeRuneEqual(left, right rune, fold bool) bool {
	if left == right || !fold {
		return left == right
	}
	return unicode.ToLower(left) == unicode.ToLower(right)
}

func mysqlLike(value, pattern string) bool {
	return likeMatch(strings.ToLower(value), strings.ToLower(pattern), '\\', false)
}
