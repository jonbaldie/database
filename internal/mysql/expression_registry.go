// This file is the closed v0.1 scalar-function registry. Every supported
// function has a fixed name, a fixed arity, and deterministic argument, NULL,
// and return-type behaviour subject to the strict-conversion rules. The registry
// is the single source of truth both text and prepared execution route through,
// so a function behaves and reports identically in either mode. A name outside
// the registry, or a call with the wrong number of arguments, fails before a
// value is produced. Its signatures are inspectable through functionSignatures.
package mysql

import (
	"math"
	"math/big"
	"sort"
	"strings"
)

// functionSpec is one registry entry. maxArgs of variadicArity marks a function
// that accepts any number of arguments at or above minArgs.
type functionSpec struct {
	minArgs int
	maxArgs int
	eval    func([]exprValue) (exprValue, error)
}

const variadicArity = -1

func (s functionSpec) acceptsArity(count int) bool {
	return count >= s.minArgs && (s.maxArgs == variadicArity || count <= s.maxArgs)
}

// scalarFunctions is the entire supported function surface. Aliases share the
// behaviour of their canonical name.
var scalarFunctions = map[string]functionSpec{
	"ABS":              {1, 1, unaryNumericFunc(absValue)},
	"CEIL":             {1, 1, unaryNumericFunc(ceilValue)},
	"CEILING":          {1, 1, unaryNumericFunc(ceilValue)},
	"FLOOR":            {1, 1, unaryNumericFunc(floorValue)},
	"ROUND":            {1, 2, roundValue},
	"POWER":            {2, 2, powerValue},
	"SQRT":             {1, 1, sqrtValue},
	"SIGN":             {1, 1, unaryNumericFunc(signValue)},
	"MOD":              {2, 2, func(a []exprValue) (exprValue, error) { return arithmetic("%", a[0], a[1]) }},
	"LENGTH":           {1, 1, unaryStringFunc(lengthValue)},
	"OCTET_LENGTH":     {1, 1, unaryStringFunc(lengthValue)},
	"CHAR_LENGTH":      {1, 1, unaryStringFunc(charLengthValue)},
	"CHARACTER_LENGTH": {1, 1, unaryStringFunc(charLengthValue)},
	"UPPER":            {1, 1, unaryStringFunc(upperValue)},
	"UCASE":            {1, 1, unaryStringFunc(upperValue)},
	"LOWER":            {1, 1, unaryStringFunc(lowerValue)},
	"LCASE":            {1, 1, unaryStringFunc(lowerValue)},
	"LTRIM":            {1, 1, unaryStringFunc(ltrimValue)},
	"RTRIM":            {1, 1, unaryStringFunc(rtrimValue)},
	"TRIM":             {1, 1, unaryStringFunc(trimValue)},
	"REVERSE":          {1, 1, unaryStringFunc(reverseValue)},
	"SUBSTRING":        {2, 3, substringValue},
	"REPLACE":          {3, 3, replaceValue},
	"LOCATE":           {2, 3, locateValue},
	"CONCAT":           {1, variadicArity, concatValue},
	"COALESCE":         {1, variadicArity, coalesceValue},
	"IFNULL":           {2, 2, ifNullValue},
	"NULLIF":           {2, 2, nullIfValue},
	"IF":               {3, 3, ifValue},
	"GREATEST":         {2, variadicArity, greatestValue},
	"LEAST":            {2, variadicArity, leastValue},
}

// callFunction dispatches a parsed function call. An unknown name and a
// wrong-arity call both fail with the MySQL identity for that error.
func callFunction(name string, arguments []exprValue) (exprValue, error) {
	spec, ok := scalarFunctions[strings.ToUpper(name)]
	if !ok {
		return exprValue{}, unknownFunctionError(name)
	}
	if !spec.acceptsArity(len(arguments)) {
		return exprValue{}, wrongArgumentCount(name)
	}
	return spec.eval(arguments)
}

// functionSignature is a registry entry's inspectable shape: its canonical name
// and the inclusive minimum and maximum argument counts, where a maximum of
// variadicArity means unbounded.
type functionSignature struct {
	name    string
	minArgs int
	maxArgs int
}

// functionSignatures returns every registered signature sorted by name, so the
// supported surface is discoverable and stable regardless of execution mode.
func functionSignatures() []functionSignature {
	signatures := make([]functionSignature, 0, len(scalarFunctions))
	for name, spec := range scalarFunctions {
		signatures = append(signatures, functionSignature{name: name, minArgs: spec.minArgs, maxArgs: spec.maxArgs})
	}
	sort.Slice(signatures, func(i, j int) bool { return signatures[i].name < signatures[j].name })
	return signatures
}

// unaryNumericFunc wraps a one-argument numeric function with NULL propagation,
// so a NULL argument returns NULL without invoking the body.
func unaryNumericFunc(body func(exprValue) (exprValue, error)) func([]exprValue) (exprValue, error) {
	return func(arguments []exprValue) (exprValue, error) {
		if arguments[0].isNull() {
			return nullValue(), nil
		}
		return body(arguments[0])
	}
}

// unaryStringFunc wraps a one-argument character function with NULL propagation
// and the strict rule that a non-character argument requires an explicit cast.
func unaryStringFunc(body func(string) (exprValue, error)) func([]exprValue) (exprValue, error) {
	return func(arguments []exprValue) (exprValue, error) {
		if arguments[0].isNull() {
			return nullValue(), nil
		}
		if arguments[0].kind != valueString {
			return exprValue{}, strictConversionError()
		}
		return body(arguments[0].s)
	}
}

func absValue(value exprValue) (exprValue, error) {
	switch value.kind {
	case valueInt:
		return absInteger(value.i)
	case valueUint:
		return value, nil
	case valueDecimal:
		return decimalValueOf(decimalValue{unscaled: new(big.Int).Abs(value.dec.unscaled), scale: value.dec.scale}), nil
	default:
		return doubleValue(math.Abs(value.f)), nil
	}
}

func absInteger(value int64) (exprValue, error) {
	if value == math.MinInt64 {
		return exprValue{}, outOfRangeValue()
	}
	if value < 0 {
		return intValue(-value), nil
	}
	return intValue(value), nil
}

func ceilValue(value exprValue) (exprValue, error) {
	return roundingValue(value, decimalCeil, math.Ceil)
}

func floorValue(value exprValue) (exprValue, error) {
	return roundingValue(value, decimalFloor, math.Floor)
}

// roundingValue applies a floor or ceiling rule in the value's domain: an
// integer is unchanged, an exact decimal rounds to an integer decimal, and an
// approximate double uses the matching floating-point rounding.
func roundingValue(value exprValue, exact func(decimalValue) decimalValue, approximate func(float64) float64) (exprValue, error) {
	switch value.kind {
	case valueInt, valueUint:
		return value, nil
	case valueDecimal:
		return decimalValueOf(exact(value.dec)), nil
	default:
		return doubleValue(approximate(value.f)), nil
	}
}

func decimalFloor(d decimalValue) decimalValue {
	quotient, remainder := decimalIntegerParts(d)
	if remainder.Sign() < 0 {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return decimalValue{unscaled: quotient, scale: 0}
}

func decimalCeil(d decimalValue) decimalValue {
	quotient, remainder := decimalIntegerParts(d)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return decimalValue{unscaled: quotient, scale: 0}
}

func decimalIntegerParts(d decimalValue) (*big.Int, *big.Int) {
	remainder := new(big.Int)
	quotient, _ := new(big.Int).QuoRem(d.unscaled, tenToThe(d.scale), remainder)
	return quotient, remainder
}

func signValue(value exprValue) (exprValue, error) {
	switch value.kind {
	case valueInt:
		return intValue(int64(intSign(value.i))), nil
	case valueUint:
		return intValue(int64(uintSign(value.u))), nil
	case valueDecimal:
		return intValue(int64(value.dec.unscaled.Sign())), nil
	default:
		return intValue(int64(floatSign(value.f))), nil
	}
}

func intSign(value int64) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}

func uintSign(value uint64) int {
	if value > 0 {
		return 1
	}
	return 0
}

func floatSign(value float64) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}

func lengthValue(text string) (exprValue, error)     { return intValue(int64(len(text))), nil }
func charLengthValue(text string) (exprValue, error) { return intValue(int64(len([]rune(text)))), nil }
func upperValue(text string) (exprValue, error)      { return stringValue(strings.ToUpper(text)), nil }
func lowerValue(text string) (exprValue, error)      { return stringValue(strings.ToLower(text)), nil }
func ltrimValue(text string) (exprValue, error)      { return stringValue(strings.TrimLeft(text, " ")), nil }
func rtrimValue(text string) (exprValue, error) {
	return stringValue(strings.TrimRight(text, " ")), nil
}
func trimValue(text string) (exprValue, error) { return stringValue(strings.Trim(text, " ")), nil }

func reverseValue(text string) (exprValue, error) {
	runes := []rune(text)
	for left, right := 0, len(runes)-1; left < right; left, right = left+1, right-1 {
		runes[left], runes[right] = runes[right], runes[left]
	}
	return stringValue(string(runes)), nil
}

// concatValue joins character arguments, propagating NULL and rejecting a
// non-character argument that would require an implicit conversion.
func concatValue(arguments []exprValue) (exprValue, error) {
	var builder strings.Builder
	for _, argument := range arguments {
		if argument.isNull() {
			return nullValue(), nil
		}
		if argument.kind != valueString {
			return exprValue{}, strictConversionError()
		}
		builder.WriteString(argument.s)
	}
	return stringValue(builder.String()), nil
}

func coalesceValue(arguments []exprValue) (exprValue, error) {
	for _, argument := range arguments {
		if !argument.isNull() {
			return argument, nil
		}
	}
	return nullValue(), nil
}

func ifNullValue(arguments []exprValue) (exprValue, error) {
	if arguments[0].isNull() {
		return arguments[1], nil
	}
	return arguments[0], nil
}

// nullIfValue returns NULL when the two arguments compare equal and the first
// argument otherwise, following MySQL's NULLIF definition.
func nullIfValue(arguments []exprValue) (exprValue, error) {
	if arguments[0].isNull() || arguments[1].isNull() {
		return arguments[0], nil
	}
	order, err := compareOperands(arguments[0], arguments[1])
	if err != nil {
		return exprValue{}, err
	}
	if order == 0 {
		return nullValue(), nil
	}
	return arguments[0], nil
}

func ifValue(arguments []exprValue) (exprValue, error) {
	known, truth, err := truthValue(arguments[0])
	if err != nil {
		return exprValue{}, err
	}
	if known && truth {
		return arguments[1], nil
	}
	return arguments[2], nil
}

func greatestValue(arguments []exprValue) (exprValue, error) {
	return extremeValue(arguments, 1)
}

func leastValue(arguments []exprValue) (exprValue, error) {
	return extremeValue(arguments, -1)
}

// extremeValue folds the arguments to the greatest (direction 1) or least
// (direction -1) value, propagating NULL and surfacing any comparison-domain
// error.
func extremeValue(arguments []exprValue, direction int) (exprValue, error) {
	best := arguments[0]
	for _, argument := range arguments {
		if argument.isNull() || best.isNull() {
			return nullValue(), nil
		}
		order, err := compareOperands(argument, best)
		if err != nil {
			return exprValue{}, err
		}
		if order*direction > 0 {
			best = argument
		}
	}
	return best, nil
}

func unknownFunctionError(name string) error {
	return sqlFailure{1305, "42000", "FUNCTION " + name + " does not exist"}
}

func wrongArgumentCount(name string) error {
	return sqlFailure{1582, "42000", "Incorrect parameter count in the call to native function '" + name + "'"}
}
