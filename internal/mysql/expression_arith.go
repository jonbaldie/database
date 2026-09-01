// This file implements the arithmetic operators and unary negation of the
// scalar expression grammar. Addition, subtraction, and multiplication stay in
// the tightest exact domain the operands share (integer, then decimal), widening
// to approximate double only when a double operand is present. True division
// produces an exact decimal or an approximate double; the integer DIV and MOD
// operators require integer operands. A NULL operand propagates, a character
// operand requires an explicit cast, and division by zero, integer overflow, or
// a non-finite double all fail.
package mysql

import (
	"math"
	"math/big"
	"strings"
)

// maxInt64AsFloat and minInt64AsFloat bound the exclusive-high, inclusive-low
// range a truncated double quotient must fall in to convert to int64 without
// overflow.
const (
	maxInt64AsFloat = float64(1 << 63)
	minInt64AsFloat = -float64(1 << 63)
)

var defaultStringType = characterType{kind: characterText, collation: collation0900AICI, wire: mysqlTypeVarString}

// compareStrings orders two strings through the default utf8mb4_0900_ai_ci
// comparison key, so equality is case- and accent-insensitive and ordering is
// deterministic.
func compareStrings(a, b string) int {
	return strings.Compare(characterComparisonKey(defaultStringType, a), characterComparisonKey(defaultStringType, b))
}

func arithmetic(operator string, a, b exprValue) (exprValue, error) {
	if a.isNull() || b.isNull() {
		return nullValue(), nil
	}
	switch operator {
	case "+", "-", "*":
		return additiveArithmetic(operator, a, b)
	case "/":
		return divideArithmetic(a, b)
	case "DIV":
		return integerDivide(a, b)
	default:
		return moduloArithmetic(a, b)
	}
}

func negateValue(value exprValue) (exprValue, error) {
	return arithmetic("-", intValue(0), value)
}

// additiveArithmetic evaluates +, -, and * in the common domain of the operands.
func additiveArithmetic(operator string, a, b exprValue) (exprValue, error) {
	domain, err := arithmeticDomain(a, b)
	if err != nil {
		return exprValue{}, err
	}
	switch domain {
	case valueDouble:
		return checkFinite(applyFloatArithmetic(operator, toFloat(a), toFloat(b)))
	case valueDecimal:
		return boundedDecimal(applyDecimalArithmetic(operator, toDecimal(a), toDecimal(b)))
	case valueUint:
		return unsignedArithmetic(operator, a, b)
	default:
		return integerArithmetic(operator, a.i, b.i)
	}
}

// arithmeticDomain resolves the tightest numeric domain that can hold the result
// of the two operands, rejecting a character operand that would need an implicit
// numeric conversion.
func arithmeticDomain(a, b exprValue) (valueKind, error) {
	if a.kind == valueString || b.kind == valueString {
		return valueNull, strictConversionError()
	}
	if a.kind == valueDouble || b.kind == valueDouble {
		return valueDouble, nil
	}
	if a.kind == valueDecimal || b.kind == valueDecimal {
		return valueDecimal, nil
	}
	if a.kind == valueUint || b.kind == valueUint {
		return valueUint, nil
	}
	return valueInt, nil
}

func applyFloatArithmetic(operator string, a, b float64) float64 {
	switch operator {
	case "+":
		return a + b
	case "-":
		return a - b
	default:
		return a * b
	}
}

func applyDecimalArithmetic(operator string, a, b decimalValue) decimalValue {
	switch operator {
	case "+":
		return addDecimal(a, b)
	case "-":
		return subtractDecimal(a, b)
	default:
		return multiplyDecimal(a, b)
	}
}

func boundedDecimal(value decimalValue) (exprValue, error) {
	if !value.withinDecimalLimits() {
		return exprValue{}, outOfRangeValue()
	}
	return decimalValueOf(value), nil
}

// integerArithmetic evaluates signed 64-bit +, -, and *, failing on overflow so
// a result never wraps.
func integerArithmetic(operator string, a, b int64) (exprValue, error) {
	result, ok := checkedInteger(operator, a, b)
	if !ok {
		return exprValue{}, outOfRangeValue()
	}
	return intValue(result), nil
}

// unsignedArithmetic evaluates exact integer arithmetic with an unsigned
// operand. MySQL keeps this domain unsigned, so a negative result or a value
// above UINT64_MAX is an out-of-range error instead of a signed or decimal
// promotion.
func unsignedArithmetic(operator string, a, b exprValue) (exprValue, error) {
	left := integerBig(a)
	right := integerBig(b)
	result := new(big.Int)
	switch operator {
	case "+":
		result.Add(left, right)
	case "-":
		result.Sub(left, right)
	default:
		result.Mul(left, right)
	}
	if result.Sign() < 0 || !result.IsUint64() {
		return exprValue{}, outOfRangeValue()
	}
	return uintValue(result.Uint64()), nil
}

func integerBig(value exprValue) *big.Int {
	if value.kind == valueUint {
		return new(big.Int).SetUint64(value.u)
	}
	return big.NewInt(value.i)
}

func checkedInteger(operator string, a, b int64) (int64, bool) {
	switch operator {
	case "+":
		return addInt64(a, b)
	case "-":
		return subInt64(a, b)
	default:
		return mulInt64(a, b)
	}
}

func addInt64(a, b int64) (int64, bool) {
	sum := a + b
	if (a > 0 && b > 0 && sum < 0) || (a < 0 && b < 0 && sum >= 0) {
		return 0, false
	}
	return sum, true
}

func subInt64(a, b int64) (int64, bool) {
	diff := a - b
	if (a >= 0 && b < 0 && diff < 0) || (a < 0 && b > 0 && diff >= 0) {
		return 0, false
	}
	return diff, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	product := a * b
	if product/b != a || (a == math.MinInt64 && b == -1) {
		return 0, false
	}
	return product, true
}

// divideArithmetic evaluates true division: an approximate quotient when a
// double operand is present, otherwise an exact decimal quotient.
func divideArithmetic(a, b exprValue) (exprValue, error) {
	if a.kind == valueString || b.kind == valueString {
		return exprValue{}, strictConversionError()
	}
	if a.kind == valueDouble || b.kind == valueDouble {
		return floatDivide(a, b)
	}
	quotient, ok := divideDecimal(toDecimal(a), toDecimal(b))
	if !ok {
		return exprValue{}, divisionByZero()
	}
	return boundedDecimal(quotient)
}

func floatDivide(a, b exprValue) (exprValue, error) {
	divisor := toFloat(b)
	if divisor == 0 {
		return exprValue{}, divisionByZero()
	}
	return checkFinite(toFloat(a) / divisor)
}

// integerDivide evaluates the integer-division operator DIV, whose result is
// always an integer: the quotient truncated toward zero across the exact
// numeric domains, or through double arithmetic when a double operand is
// present. A character operand requires an explicit cast.
func integerDivide(a, b exprValue) (exprValue, error) {
	if a.kind == valueString || b.kind == valueString {
		return exprValue{}, strictConversionError()
	}
	if a.kind == valueDouble || b.kind == valueDouble {
		return floatIntegerDivide(a, b)
	}
	quotient, ok := truncatedQuotient(toDecimal(a), toDecimal(b))
	if !ok {
		return exprValue{}, divisionByZero()
	}
	if !quotient.IsInt64() {
		return exprValue{}, outOfRangeValue()
	}
	return intValue(quotient.Int64()), nil
}

func floatIntegerDivide(a, b exprValue) (exprValue, error) {
	divisor := toFloat(b)
	if divisor == 0 {
		return exprValue{}, divisionByZero()
	}
	quotient := math.Trunc(toFloat(a) / divisor)
	if math.IsInf(quotient, 0) || math.IsNaN(quotient) || quotient >= maxInt64AsFloat || quotient < minInt64AsFloat {
		return exprValue{}, outOfRangeValue()
	}
	return intValue(int64(quotient)), nil
}

// moduloArithmetic evaluates the modulus operator (% and MOD), preserving the
// operand domain: integer operands yield an integer remainder, exact operands an
// exact decimal remainder, and a double operand a double remainder. The result
// takes the sign of the dividend, matching MySQL.
func moduloArithmetic(a, b exprValue) (exprValue, error) {
	if a.kind == valueString || b.kind == valueString {
		return exprValue{}, strictConversionError()
	}
	if a.kind == valueDouble || b.kind == valueDouble {
		return floatModulo(a, b)
	}
	if dividend, divisor, ok := integerPair(a, b); ok {
		return integerModulo(dividend, divisor)
	}
	return decimalModulo(toDecimal(a), toDecimal(b))
}

func floatModulo(a, b exprValue) (exprValue, error) {
	divisor := toFloat(b)
	if divisor == 0 {
		return exprValue{}, divisionByZero()
	}
	return checkFinite(math.Mod(toFloat(a), divisor))
}

func integerModulo(dividend, divisor int64) (exprValue, error) {
	if divisor == 0 {
		return exprValue{}, divisionByZero()
	}
	return intValue(dividend % divisor), nil
}

// decimalModulo computes an exact decimal remainder as dividend minus divisor
// times the truncated integer quotient.
func decimalModulo(a, b decimalValue) (exprValue, error) {
	quotient, ok := truncatedQuotient(a, b)
	if !ok {
		return exprValue{}, divisionByZero()
	}
	product := multiplyDecimal(b, decimalValue{unscaled: quotient, scale: 0})
	return boundedDecimal(subtractDecimal(a, product))
}

// truncatedQuotient returns the integer part of a divided by b, truncated toward
// zero and exact. The second result is false when the divisor is zero.
func truncatedQuotient(a, b decimalValue) (*big.Int, bool) {
	numerator := new(big.Int).Mul(a.unscaled, tenToThe(b.scale))
	denominator := new(big.Int).Mul(b.unscaled, tenToThe(a.scale))
	if denominator.Sign() == 0 {
		return nil, false
	}
	return new(big.Int).Quo(numerator, denominator), true
}

// integerPair reports whether both operands are exact integers within the signed
// 64-bit range, the case an integer remainder covers directly.
func integerPair(a, b exprValue) (int64, int64, bool) {
	dividend, okA := asInt64(a)
	divisor, okB := asInt64(b)
	return dividend, divisor, okA && okB
}

func asInt64(value exprValue) (int64, bool) {
	switch value.kind {
	case valueInt:
		return value.i, true
	case valueUint:
		if value.u <= math.MaxInt64 {
			return int64(value.u), true
		}
		return 0, false
	default:
		return 0, false
	}
}
