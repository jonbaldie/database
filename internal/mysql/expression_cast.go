// This file implements the explicit CAST(expr AS type) and CONVERT(expr, type)
// forms. An explicit cast is the deliberate, documented representation change
// the strict-conversion rules otherwise forbid, so it is the only path from a
// character value to a number, a temporal value, or raw binary, and between the
// exact and approximate numeric domains. The finite target vocabulary is SIGNED,
// UNSIGNED, DECIMAL, CHAR, BINARY, DOUBLE, REAL, FLOAT, DATE, DATETIME, TIME,
// and YEAR; an unknown target, an out-of-range result, a malformed temporal
// spelling, or a character value that is not a clean number for its numeric
// target fails before producing a value.
package mysql

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const defaultCastDecimalPrecision = 10

// castTarget is a parsed cast destination type. kind selects the domain; the
// precision, scale, and length fields carry the parenthesised arguments the
// DECIMAL and CHAR targets accept, a non-zero temporal kind selects a temporal
// domain within the character representation.
type castTarget struct {
	kind      valueKind
	precision int
	scale     int
	length    int
	bounded   bool
	temporal  temporalKind
	single    bool
	binary    bool
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

// castTargetParsers maps a cast destination name to the parser that reads its
// optional parenthesised arguments.
var castTargetParsers = map[string]func(*exprParser) (castTarget, error){
	"SIGNED":   signedCastTarget,
	"UNSIGNED": unsignedCastTarget,
	"DECIMAL":  decimalCastTarget,
	"DEC":      decimalCastTarget,
	"NUMERIC":  decimalCastTarget,
	"CHAR":     charCastTarget,
	"BINARY":   binaryCastTarget,
	"DOUBLE":   doubleCastTarget,
	"REAL":     doubleCastTarget,
	"FLOAT":    floatCastTarget,
	"DATE":     dateCastTarget,
	"DATETIME": datetimeCastTarget,
	"TIME":     timeCastTarget,
	"YEAR":     yearCastTarget,
}

// parseCastTarget reads the destination type name and any parenthesised
// arguments into a castTarget.
func parseCastTarget(p *exprParser) (castTarget, error) {
	token, ok := p.peek()
	if !ok || token.kind != tokenIdent {
		return castTarget{}, unsupportedExpression()
	}
	p.advance()
	parser, ok := castTargetParsers[strings.ToUpper(token.text)]
	if !ok {
		return castTarget{}, unsupportedExpression()
	}
	return parser(p)
}

func signedCastTarget(p *exprParser) (castTarget, error) {
	p.matchKeyword("INTEGER")
	return castTarget{kind: valueInt}, nil
}

func unsignedCastTarget(p *exprParser) (castTarget, error) {
	p.matchKeyword("INTEGER")
	return castTarget{kind: valueUint}, nil
}

func doubleCastTarget(p *exprParser) (castTarget, error) {
	return unboundedNumericCastTarget(p, false)
}

func floatCastTarget(p *exprParser) (castTarget, error) {
	return unboundedNumericCastTarget(p, true)
}

func dateCastTarget(p *exprParser) (castTarget, error) {
	return unboundedCastTarget(p, temporalDate)
}

func yearCastTarget(p *exprParser) (castTarget, error) {
	return unboundedCastTarget(p, temporalYear)
}

func datetimeCastTarget(p *exprParser) (castTarget, error) {
	return temporalPrecisionCastTarget(p, temporalDatetime)
}

func timeCastTarget(p *exprParser) (castTarget, error) {
	return temporalPrecisionCastTarget(p, temporalTime)
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

// temporalPrecisionCastTarget reads the DATETIME and TIME destinations with
// their optional fractional-second precision, which follows the same
// zero-through-six ceiling the temporal columns enforce.
func temporalPrecisionCastTarget(p *exprParser, kind temporalKind) (castTarget, error) {
	precision, _, bounded, err := readTypeArguments(p)
	if err != nil {
		return castTarget{}, err
	}
	if !bounded {
		return castTarget{kind: valueString, temporal: kind}, nil
	}
	if precision < 0 || precision > 6 {
		return castTarget{}, sqlFailure{1426, "42000", fmt.Sprintf("Too-big precision %d specified for column. Maximum is 6", precision)}
	}
	return castTarget{kind: valueString, temporal: kind, precision: precision}, nil
}

// unboundedCastTarget reads a temporal destination that accepts no
// parenthesised arguments.
func unboundedCastTarget(p *exprParser, kind temporalKind) (castTarget, error) {
	if _, _, bounded, err := readTypeArguments(p); err != nil || bounded {
		return castTarget{}, unsupportedExpression()
	}
	return castTarget{kind: valueString, temporal: kind}, nil
}

// binaryCastTarget reads the BINARY destination with its optional length. A
// binary value keeps the byte representation and is padded with zero bytes to
// a bounded length.
func binaryCastTarget(p *exprParser) (castTarget, error) {
	length, _, bounded, err := readTypeArguments(p)
	if err != nil {
		return castTarget{}, err
	}
	return castTarget{kind: valueString, length: length, bounded: bounded, binary: true}, nil
}

// unboundedNumericCastTarget reads an approximate-numeric destination that
// accepts no parenthesised arguments. FLOAT is the single-precision spelling;
// DOUBLE and REAL convert in the double-precision domain.
func unboundedNumericCastTarget(p *exprParser, single bool) (castTarget, error) {
	if _, _, bounded, err := readTypeArguments(p); err != nil || bounded {
		return castTarget{}, unsupportedExpression()
	}
	return castTarget{kind: valueDouble, single: single}, nil
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
	case valueDouble:
		return castToDouble(value, target.single)
	default:
		if target.temporal != temporalNone {
			return castToTemporal(value, target)
		}
		if target.binary {
			return castToBinary(value, target.length, target.bounded)
		}
		return castToChar(value, target.length, target.bounded)
	}
}

// castToBinary renders the value as bytes, padding a bounded target with zero
// bytes when the value is shorter and failing when it is longer rather than
// silently truncating.
func castToBinary(value exprValue, length int, bounded bool) (exprValue, error) {
	rendered := value.render()
	if bounded {
		if len(rendered) > length {
			return exprValue{}, incorrectCastValue(rendered)
		}
		rendered += strings.Repeat("\x00", length-len(rendered))
	}
	return exprValue{kind: valueString, s: rendered, binary: true}, nil
}

// castToTemporal validates the rendered value against the target temporal
// family and returns its canonical spelling tagged with that family, exactly
// as the same spelling is validated for a temporal column write. A malformed
// or out-of-range value fails.
func castToTemporal(value exprValue, target castTarget) (exprValue, error) {
	typ := temporalTypeForKind(target.temporal, target.precision)
	text := castTemporalSource(value.temporal, target.temporal, value.render())
	canonical, err := castTemporalValue(typ, text)
	if err != nil {
		var failure sqlFailure
		if errors.As(err, &failure) && failure.code == 1292 {
			return exprValue{}, incorrectCastTemporal(target.temporal, text)
		}
		return exprValue{}, err
	}
	return exprValue{kind: valueString, s: canonical, temporal: target.temporal, precision: target.precision}, nil
}

func incorrectCastTemporal(kind temporalKind, text string) error {
	return sqlFailure{1292, "22007", fmt.Sprintf("Incorrect %s value: '%s'", temporalLabel(kind), text)}
}

// castToDouble converts any numeric value to an approximate double, or a
// character value that spells one number. A FLOAT target rounds through single
// precision, the documented rounding its representation change requests. A
// malformed spelling fails, and a non-finite result fails rather than
// saturating.
func castToDouble(value exprValue, single bool) (exprValue, error) {
	parsed, err := castFloatInput(value)
	if err != nil {
		return exprValue{}, err
	}
	if math.IsNaN(parsed) {
		return exprValue{}, incorrectCastValue(value.render())
	}
	if math.IsInf(parsed, 0) {
		return exprValue{}, nonFiniteResult()
	}
	if single {
		parsed = float64(float32(parsed))
		if math.IsInf(parsed, 0) {
			return exprValue{}, nonFiniteResult()
		}
	}
	return exprValue{kind: valueDouble, f: parsed, single: single}, nil
}

// castFloatInput reads the approximate input. A character value must spell one
// number in the engine's literal grammar; anything else fails.
func castFloatInput(value exprValue) (float64, error) {
	switch value.kind {
	case valueInt:
		return float64(value.i), nil
	case valueUint:
		return float64(value.u), nil
	case valueDecimal:
		return strconv.ParseFloat(value.dec.renderDecimal(), 64)
	case valueDouble:
		return value.f, nil
	default:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value.s), 64)
		if err != nil {
			return 0, incorrectCastValue(value.s)
		}
		return parsed, nil
	}
}

// castTemporalSource adapts a temporal input to the spelling its target family
// validates: a DATETIME or TIMESTAMP contributes its date or clock part, and a
// DATE contributes its own spelling or a midnight clock. A character input
// passes through unchanged, so only the documented canonical spellings cast.
func castTemporalSource(source, target temporalKind, text string) string {
	if source == temporalDate {
		if target == temporalDatetime || target == temporalTimestamp {
			return text + " 00:00:00"
		}
		if target == temporalTime {
			return "00:00:00"
		}
	}
	if source == temporalDatetime || source == temporalTimestamp {
		return castDatetimeSource(target, text)
	}
	return text
}

// castDatetimeSource contributes a DATETIME spelling's date or clock part to a
// DATE or TIME target, leaving every other target to the full spelling.
func castDatetimeSource(target temporalKind, text string) string {
	if target != temporalDate && target != temporalTime {
		return text
	}
	datePart, clockPart, _ := strings.Cut(text, " ")
	if target == temporalTime {
		return clockPart
	}
	return datePart
}

// castTemporalValue validates a cast input against one temporal family. Unlike
// the column-literal path it never treats the text 'null' as a NULL keyword,
// because the expression engine represents NULL before the cast runs.
func castTemporalValue(typ temporalType, text string) (string, error) {
	switch typ.kind {
	case temporalDate:
		return canonicalDateValue(text, "", 0)
	case temporalTime:
		return canonicalTimeValue(typ, text, "", 0)
	case temporalDatetime:
		return canonicalDatetimeValue(typ, text, "", 0, false)
	case temporalYear:
		return canonicalYearValue(text, "", 0)
	default:
		return "", unsupportedExpression()
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
