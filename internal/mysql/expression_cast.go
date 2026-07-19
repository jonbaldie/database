// This file implements the explicit CAST(expr AS type) and CONVERT(expr, type)
// forms. An explicit cast is the deliberate, documented representation change
// the strict-conversion rules otherwise forbid, so it is the only path from a
// character value to a number or between the exact and approximate numeric
// domains. The finite target vocabulary is SIGNED, UNSIGNED, DECIMAL, and CHAR;
// an unknown target, an out-of-range result, or a character value that is not a
// clean number for its numeric target fails before producing a value.
package mysql

import (
	"strconv"
	"strings"
)

const defaultCastDecimalPrecision = 10

// castTarget is a parsed cast destination type. kind selects the domain; the
// precision, scale, and length fields carry the parenthesised arguments the
// DECIMAL and CHAR targets accept.
type castTarget struct {
	kind      valueKind
	precision int
	scale     int
	length    int
	bounded   bool
}

// parseCast consumes CAST ( expr AS type ). The CAST identifier is current.
func parseCast(p *exprParser) (exprValue, error) {
	p.advance() // CAST
	if !matchLParen(p) {
		return exprValue{}, unsupportedExpression()
	}
	value, err := p.parseExpression()
	if err != nil {
		return exprValue{}, err
	}
	if !p.matchKeyword("AS") {
		return exprValue{}, unsupportedExpression()
	}
	return finishCast(p, value)
}

// parseConvert consumes CONVERT ( expr , type ). CONVERT ... USING is a charset
// conversion outside the v0.1 expression surface and is not accepted here.
func parseConvert(p *exprParser) (exprValue, error) {
	p.advance() // CONVERT
	if !matchLParen(p) {
		return exprValue{}, unsupportedExpression()
	}
	value, err := p.parseExpression()
	if err != nil {
		return exprValue{}, err
	}
	if !matchComma(p) {
		return exprValue{}, unsupportedExpression()
	}
	return finishCast(p, value)
}

func finishCast(p *exprParser, value exprValue) (exprValue, error) {
	target, err := parseCastTarget(p)
	if err != nil {
		return exprValue{}, err
	}
	if !matchRParen(p) {
		return exprValue{}, unsupportedExpression()
	}
	return evalCast(value, target)
}

// parseCastTarget reads the destination type name and any parenthesised
// arguments into a castTarget.
func parseCastTarget(p *exprParser) (castTarget, error) {
	token, ok := p.peek()
	if !ok || token.kind != tokenIdent {
		return castTarget{}, unsupportedExpression()
	}
	p.advance()
	switch strings.ToUpper(token.text) {
	case "SIGNED":
		p.matchKeyword("INTEGER")
		return castTarget{kind: valueInt}, nil
	case "UNSIGNED":
		p.matchKeyword("INTEGER")
		return castTarget{kind: valueUint}, nil
	case "DECIMAL", "DEC", "NUMERIC":
		return decimalCastTarget(p)
	case "CHAR":
		return charCastTarget(p)
	default:
		return castTarget{}, unsupportedExpression()
	}
}

func decimalCastTarget(p *exprParser) (castTarget, error) {
	precision, scale, bounded, err := readTypeArguments(p)
	if err != nil {
		return castTarget{}, err
	}
	if !bounded {
		return castTarget{kind: valueDecimal, precision: defaultCastDecimalPrecision, scale: 0}, nil
	}
	if err := validateDecimalCeiling(precision, scale); err != nil {
		return castTarget{}, err
	}
	return castTarget{kind: valueDecimal, precision: precision, scale: scale}, nil
}

func charCastTarget(p *exprParser) (castTarget, error) {
	length, _, bounded, err := readTypeArguments(p)
	if err != nil {
		return castTarget{}, err
	}
	return castTarget{kind: valueString, length: length, bounded: bounded}, nil
}

// readTypeArguments reads an optional ( first [, second] ) argument list of
// non-negative integers, returning whether any argument list was present.
func readTypeArguments(p *exprParser) (int, int, bool, error) {
	if !matchLParen(p) {
		return 0, 0, false, nil
	}
	first, err := readIntegerToken(p)
	if err != nil {
		return 0, 0, false, err
	}
	second := 0
	if matchComma(p) {
		second, err = readIntegerToken(p)
		if err != nil {
			return 0, 0, false, err
		}
	}
	if !matchRParen(p) {
		return 0, 0, false, unsupportedExpression()
	}
	return first, second, true, nil
}

func readIntegerToken(p *exprParser) (int, error) {
	token, ok := p.peek()
	if !ok || token.kind != tokenNumber {
		return 0, unsupportedExpression()
	}
	value, err := strconv.Atoi(token.text)
	if err != nil {
		return 0, unsupportedExpression()
	}
	p.advance()
	return value, nil
}

// evalCast performs the representation change. A NULL casts to NULL in every
// target.
func evalCast(value exprValue, target castTarget) (exprValue, error) {
	if value.isNull() {
		return nullValue(), nil
	}
	switch target.kind {
	case valueInt:
		return castToSigned(value)
	case valueUint:
		return castToUnsigned(value)
	case valueDecimal:
		return castToDecimal(value, target.precision, target.scale)
	default:
		return castToChar(value, target.length, target.bounded)
	}
}

func castToSigned(value exprValue) (exprValue, error) {
	decimal, err := decimalForCast(value)
	if err != nil {
		return exprValue{}, err
	}
	result, ok := decimal.toInt64()
	if !ok {
		return exprValue{}, outOfRangeValue()
	}
	return intValue(result), nil
}

func castToUnsigned(value exprValue) (exprValue, error) {
	decimal, err := decimalForCast(value)
	if err != nil {
		return exprValue{}, err
	}
	result, ok := decimal.toUint64()
	if !ok {
		return exprValue{}, outOfRangeValue()
	}
	return uintValue(result), nil
}

func castToDecimal(value exprValue, precision, scale int) (exprValue, error) {
	decimal, err := decimalForCast(value)
	if err != nil {
		return exprValue{}, err
	}
	rounded := roundDecimalToScale(decimal, scale)
	if rounded.integerDigits() > precision-scale {
		return exprValue{}, outOfRangeValue()
	}
	return decimalValueOf(rounded), nil
}

// decimalForCast converts any castable value to an exact decimal. A character
// value must spell a plain decimal number; an approximate double is converted
// through its shortest decimal spelling.
func decimalForCast(value exprValue) (decimalValue, error) {
	switch value.kind {
	case valueInt:
		return decimalFromInt(value.i), nil
	case valueUint:
		parsed, _ := parseDecimalText(strconv.FormatUint(value.u, 10))
		return parsed, nil
	case valueDecimal:
		return value.dec, nil
	case valueDouble:
		return parseDecimalForCast(strconv.FormatFloat(value.f, 'f', -1, 64))
	default:
		return parseDecimalForCast(value.s)
	}
}

func parseDecimalForCast(text string) (decimalValue, error) {
	parsed, ok := parseDecimalText(strings.TrimSpace(text))
	if !ok {
		return decimalValue{}, incorrectCastValue(text)
	}
	if !parsed.withinDecimalCeiling() {
		return decimalValue{}, outOfRangeValue()
	}
	return parsed, nil
}

// castToChar renders the value as text, failing when a bounded target is shorter
// than the value rather than silently truncating.
func castToChar(value exprValue, length int, bounded bool) (exprValue, error) {
	rendered := value.render()
	if bounded && len([]rune(rendered)) > length {
		return exprValue{}, incorrectCastValue(rendered)
	}
	return stringValue(rendered), nil
}

func incorrectCastValue(text string) error {
	return sqlFailure{1292, "22007", "Truncated incorrect value: '" + text + "'"}
}
