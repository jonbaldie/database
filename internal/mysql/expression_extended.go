package mysql

import (
	"math"
	"strings"
)

// roundValue implements the finite numeric ROUND function. Exact values keep
// decimal arithmetic and round half away from zero; approximate values use the
// same rule in the double domain. A NULL argument remains NULL and character
// input requires an explicit cast.
func roundValue(arguments []exprValue) (exprValue, error) {
	if roundHasNull(arguments) {
		return nullValue(), nil
	}
	if !isNumericKind(arguments[0].kind) {
		return exprValue{}, strictConversionError()
	}
	position, err := roundPosition(arguments)
	if err != nil {
		return exprValue{}, err
	}
	return roundNumeric(arguments[0], position)
}

func roundHasNull(arguments []exprValue) bool {
	if arguments[0].isNull() {
		return true
	}
	return len(arguments) == 2 && arguments[1].isNull()
}

func roundPosition(arguments []exprValue) (int, error) {
	if len(arguments) == 1 {
		return 0, nil
	}
	places, err := expressionInteger(arguments[1])
	if err != nil {
		return 0, err
	}
	if places > int64(math.MaxInt) || places < int64(math.MinInt) {
		return 0, outOfRangeValue()
	}
	return int(places), nil
}

func roundNumeric(value exprValue, position int) (exprValue, error) {
	switch value.kind {
	case valueDouble:
		return roundDouble(value.f, position)
	case valueInt, valueUint:
		if position >= 0 {
			return value, nil
		}
		return boundedDecimal(roundExact(toDecimal(value), position))
	default:
		return boundedDecimal(roundExact(value.dec, position))
	}
}

func roundDouble(value float64, places int) (exprValue, error) {
	if !isFinite(value) {
		return exprValue{}, nonFiniteResult()
	}
	if places > 308 {
		return doubleValue(value), nil
	}
	if places < -308 {
		return doubleValue(0), nil
	}
	if places >= 0 {
		factor := math.Pow10(places)
		if factor == 0 || math.IsInf(factor, 0) || math.Abs(value) > math.MaxFloat64/factor {
			return doubleValue(value), nil
		}
		return checkFinite(math.Round(value*factor) / factor)
	}
	factor := math.Pow10(-places)
	return checkFinite(math.Round(value/factor) * factor)
}

func roundExact(value decimalValue, places int) decimalValue {
	if places >= 0 {
		return roundDecimalToScale(value, places)
	}
	if places < -128 {
		return decimalFromInt(0)
	}
	magnitude := -places
	if value.scale+magnitude > 256 {
		return decimalFromInt(0)
	}
	quotient := roundedQuotient(value.unscaled, tenToThe(value.scale+magnitude))
	return decimalValue{unscaled: quotient.Mul(quotient, tenToThe(magnitude)), scale: 0}
}

func powerValue(arguments []exprValue) (exprValue, error) {
	if arguments[0].isNull() || arguments[1].isNull() {
		return nullValue(), nil
	}
	if !isNumericKind(arguments[0].kind) || !isNumericKind(arguments[1].kind) {
		return exprValue{}, strictConversionError()
	}
	return checkFinite(math.Pow(expressionFloat(arguments[0]), expressionFloat(arguments[1])))
}

func sqrtValue(arguments []exprValue) (exprValue, error) {
	if arguments[0].isNull() {
		return nullValue(), nil
	}
	if !isNumericKind(arguments[0].kind) {
		return exprValue{}, strictConversionError()
	}
	value := expressionFloat(arguments[0])
	if value < 0 {
		return exprValue{}, outOfRangeValue()
	}
	return checkFinite(math.Sqrt(value))
}

func expressionFloat(value exprValue) float64 {
	switch value.kind {
	case valueInt:
		return float64(value.i)
	case valueUint:
		return float64(value.u)
	case valueDecimal:
		return toFloat(value)
	default:
		return value.f
	}
}

func expressionInteger(value exprValue) (int64, error) {
	if !isNumericKind(value.kind) {
		return 0, strictConversionError()
	}
	switch value.kind {
	case valueInt:
		return value.i, nil
	case valueUint:
		return integerFromUnsigned(value.u)
	case valueDecimal:
		return integerFromDecimal(value.dec)
	default:
		return integerFromDouble(value.f)
	}
}

func integerFromUnsigned(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, outOfRangeValue()
	}
	return int64(value), nil
}

func integerFromDecimal(value decimalValue) (int64, error) {
	result, ok := value.toInt64()
	if !ok {
		return 0, outOfRangeValue()
	}
	return result, nil
}

func integerFromDouble(value float64) (int64, error) {
	if !isFinite(value) {
		return 0, outOfRangeValue()
	}
	rounded := math.Round(value)
	if rounded >= maxInt64AsFloat || rounded < minInt64AsFloat {
		return 0, outOfRangeValue()
	}
	return int64(rounded), nil
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func stringArgument(value exprValue) (string, error) {
	if value.kind != valueString {
		return "", strictConversionError()
	}
	return value.s, nil
}

func substringValue(arguments []exprValue) (exprValue, error) {
	if hasNullArgument(arguments) {
		return nullValue(), nil
	}
	text, err := stringArgument(arguments[0])
	if err != nil {
		return exprValue{}, err
	}
	position, err := expressionInteger(arguments[1])
	if err != nil {
		return exprValue{}, err
	}
	if position == 0 {
		return stringValue(""), nil
	}
	runes := []rune(text)
	start, end, err := substringBounds(len(runes), position, arguments)
	if err != nil {
		return exprValue{}, err
	}
	return stringValue(string(runes[start:end])), nil
}

func hasNullArgument(arguments []exprValue) bool {
	for _, argument := range arguments {
		if argument.isNull() {
			return true
		}
	}
	return false
}

func substringBounds(length int, position int64, arguments []exprValue) (int, int, error) {
	start := substringStart(length, position)
	if start >= length || len(arguments) == 2 {
		return start, length, nil
	}
	partLength, err := expressionInteger(arguments[2])
	if err != nil {
		return 0, 0, err
	}
	if partLength <= 0 {
		return start, start, nil
	}
	remaining := int64(length - start)
	if partLength >= remaining {
		return start, length, nil
	}
	return start, start + int(partLength), nil
}

func substringStart(length int, position int64) int {
	if position > 0 {
		if position > int64(length) {
			return length
		}
		return int(position - 1)
	}
	start := int64(length) + position
	if start < 0 {
		return 0
	}
	return int(start)
}

func replaceValue(arguments []exprValue) (exprValue, error) {
	for _, argument := range arguments {
		if argument.isNull() {
			return nullValue(), nil
		}
	}
	text, err := stringArgument(arguments[0])
	if err != nil {
		return exprValue{}, err
	}
	search, err := stringArgument(arguments[1])
	if err != nil {
		return exprValue{}, err
	}
	replacement, err := stringArgument(arguments[2])
	if err != nil {
		return exprValue{}, err
	}
	if search == "" {
		return stringValue(text), nil
	}
	return stringValue(strings.ReplaceAll(text, search, replacement)), nil
}

func locateValue(arguments []exprValue) (exprValue, error) {
	if hasNullArgument(arguments) {
		return nullValue(), nil
	}
	needle, err := stringArgument(arguments[0])
	if err != nil {
		return exprValue{}, err
	}
	haystack, err := stringArgument(arguments[1])
	if err != nil {
		return exprValue{}, err
	}
	start, err := locateStart(arguments)
	if err != nil {
		return exprValue{}, err
	}
	if start <= 0 {
		return intValue(0), nil
	}
	haystackRunes, needleRunes := []rune(haystack), []rune(needle)
	startIndex := start - 1
	if startIndex >= int64(len(haystackRunes)) {
		return intValue(0), nil
	}
	needleLength, haystackLength := len(needleRunes), len(haystackRunes)
	for index := int(startIndex); index+needleLength <= haystackLength; index++ {
		if runesEqual(haystackRunes[index:index+needleLength], needleRunes) {
			return intValue(int64(index + 1)), nil
		}
	}
	return intValue(0), nil
}

func locateStart(arguments []exprValue) (int64, error) {
	if len(arguments) == 2 {
		return 1, nil
	}
	return expressionInteger(arguments[2])
}

func runesEqual(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
