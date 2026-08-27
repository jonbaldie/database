// This file evaluates the operators of the scalar expression grammar: the
// three-valued logical connectives, the comparison operators, the arithmetic
// operators, unary negation, and the postfix IS predicates. Every operator
// follows MySQL's NULL propagation and the v0.1 strict-conversion rules, so an
// implicit character-to-numeric use, a division by zero, an integer overflow, or
// a non-finite floating result fails rather than silently coercing, wrapping, or
// saturating.
package mysql

import (
	"math"
	"strconv"
	"strings"
)

func isNumericKind(kind valueKind) bool {
	return kind == valueInt || kind == valueUint || kind == valueDecimal || kind == valueDouble
}

// truthValue coerces a value to a MySQL boolean for logical and IS-truth
// contexts. A NULL is unknown; a numeric value is true when non-zero; a
// character value requires an explicit cast and fails.
func truthValue(value exprValue) (known bool, truth bool, err error) {
	switch value.kind {
	case valueNull:
		return false, false, nil
	case valueInt:
		return true, value.i != 0, nil
	case valueUint:
		return true, value.u != 0, nil
	case valueDecimal:
		return true, value.dec.unscaled.Sign() != 0, nil
	case valueDouble:
		return true, value.f != 0, nil
	default:
		return false, false, strictConversionError()
	}
}

func logicalAnd(a, b exprValue) (exprValue, error) {
	left, right, err := bothTruths(a, b)
	if err != nil {
		return exprValue{}, err
	}
	if left.definiteFalse() || right.definiteFalse() {
		return boolValue(false), nil
	}
	if !left.known || !right.known {
		return nullValue(), nil
	}
	return boolValue(true), nil
}

func logicalOr(a, b exprValue) (exprValue, error) {
	left, right, err := bothTruths(a, b)
	if err != nil {
		return exprValue{}, err
	}
	if left.definiteTrue() || right.definiteTrue() {
		return boolValue(true), nil
	}
	if !left.known || !right.known {
		return nullValue(), nil
	}
	return boolValue(false), nil
}

func logicalXor(a, b exprValue) (exprValue, error) {
	left, right, err := bothTruths(a, b)
	if err != nil {
		return exprValue{}, err
	}
	if !left.known || !right.known {
		return nullValue(), nil
	}
	return boolValue(left.truth != right.truth), nil
}

func logicalNot(a exprValue) (exprValue, error) {
	known, truth, err := truthValue(a)
	if err != nil {
		return exprValue{}, err
	}
	if !known {
		return nullValue(), nil
	}
	return boolValue(!truth), nil
}

// ternaryTruth is a resolved three-valued-logic operand: known reports whether
// the truth value is definite, and truth is meaningful only when known.
type ternaryTruth struct {
	known bool
	truth bool
}

func (t ternaryTruth) definiteTrue() bool  { return t.known && t.truth }
func (t ternaryTruth) definiteFalse() bool { return t.known && !t.truth }

func bothTruths(a, b exprValue) (ternaryTruth, ternaryTruth, error) {
	leftKnown, leftTruth, err := truthValue(a)
	if err != nil {
		return ternaryTruth{}, ternaryTruth{}, err
	}
	rightKnown, rightTruth, err := truthValue(b)
	if err != nil {
		return ternaryTruth{}, ternaryTruth{}, err
	}
	return ternaryTruth{leftKnown, leftTruth}, ternaryTruth{rightKnown, rightTruth}, nil
}

func isNullPredicate(value exprValue, negate bool) exprValue {
	return boolValue(value.isNull() != negate)
}

func isUnknownPredicate(value exprValue, negate bool) exprValue {
	return boolValue(value.isNull() != negate)
}

func isTruthPredicate(value exprValue, wantTrue, negate bool) (exprValue, error) {
	known, truth, err := truthValue(value)
	if err != nil {
		return exprValue{}, err
	}
	matches := known && truth == wantTrue
	return boolValue(matches != negate), nil
}

// compareValues applies a comparison operator with MySQL NULL semantics: the
// null-safe operator never yields NULL, while every other operator yields NULL
// when either operand is NULL.
func compareValues(operator string, a, b exprValue) (exprValue, error) {
	if operator == "<=>" {
		return nullSafeEqual(a, b)
	}
	if a.isNull() || b.isNull() {
		return nullValue(), nil
	}
	order, err := compareOperands(a, b)
	if err != nil {
		return exprValue{}, err
	}
	return boolValue(applyComparison(operator, order)), nil
}

func nullSafeEqual(a, b exprValue) (exprValue, error) {
	if a.isNull() || b.isNull() {
		return boolValue(a.isNull() && b.isNull()), nil
	}
	order, err := compareOperands(a, b)
	if err != nil {
		return exprValue{}, err
	}
	return boolValue(order == 0), nil
}

func applyComparison(operator string, order int) bool {
	switch operator {
	case "=":
		return order == 0
	case "<>", "!=":
		return order != 0
	case "<":
		return order < 0
	case "<=":
		return order <= 0
	case ">":
		return order > 0
	default:
		return order >= 0
	}
}

// compareOperands returns the sign of a minus b within a common domain. Two
// strings compare through the default collation key; a string against a number
// requires an explicit cast; otherwise the operands promote to a common numeric
// domain, using approximate comparison only when an approximate operand is
// present.
func compareOperands(a, b exprValue) (int, error) {
	if a.temporal != temporalNone && b.temporal != temporalNone {
		return strings.Compare(temporalComparisonKey(a), temporalComparisonKey(b)), nil
	}
	if a.kind == valueString || b.kind == valueString {
		if a.kind == valueString && b.kind == valueString {
			return compareStrings(a.s, b.s), nil
		}
		return 0, strictConversionError()
	}
	if a.kind == valueDouble || b.kind == valueDouble {
		return compareFloat(toFloat(a), toFloat(b)), nil
	}
	return compareDecimal(toDecimal(a), toDecimal(b)), nil
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// toFloat converts any numeric value to its nearest double for approximate
// comparison.
func toFloat(value exprValue) float64 {
	switch value.kind {
	case valueInt:
		return float64(value.i)
	case valueUint:
		return float64(value.u)
	case valueDouble:
		return value.f
	default:
		parsed, _ := strconv.ParseFloat(value.dec.renderDecimal(), 64)
		return parsed
	}
}

// toDecimal converts an exact numeric value to a decimal for exact comparison.
func toDecimal(value exprValue) decimalValue {
	switch value.kind {
	case valueInt:
		return decimalFromInt(value.i)
	case valueUint:
		parsed, _ := parseDecimalText(strconv.FormatUint(value.u, 10))
		return parsed
	default:
		return value.dec
	}
}

func strictConversionError() error {
	return sqlFailure{1292, "22007", "explicit cast required for this conversion"}
}

func outOfRangeValue() error {
	return sqlFailure{1690, "22003", "value is out of range"}
}

func divisionByZero() error {
	return sqlFailure{1365, "22012", "Division by 0"}
}

func nonFiniteResult() error {
	return sqlFailure{1690, "22003", "DOUBLE value is out of range"}
}

func unknownColumnError(name string) error {
	return sqlFailure{1054, "42S22", "Unknown column '" + name + "' in 'field list'"}
}

func checkFinite(value float64) (exprValue, error) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return exprValue{}, nonFiniteResult()
	}
	return doubleValue(value), nil
}
