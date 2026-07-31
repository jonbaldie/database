// This file implements the exact fixed-point DECIMAL domain the v0.1 expression
// engine uses for exact numeric literals, comparison, cast, and arithmetic. A
// decimal is an arbitrary-precision unscaled integer paired with a decimal
// scale, so 0.1 + 0.2 is exactly 0.3 rather than a binary approximation.
// Multiplication and addition are exact; division rounds half away from zero at
// a documented result scale so the outcome is deterministic. A result whose
// significant digits exceed the v0.1 DECIMAL precision ceiling of 65 fails as an
// out-of-range value rather than silently wrapping.
package mysql

import (
	"math/big"
	"strings"
)

const decimalPrecisionCeiling = 65

const decimalScaleCeiling = 30

// divisionScaleIncrement is the number of fractional digits division adds beyond
// the dividend's scale, matching MySQL's default div_precision_increment so a
// quotient like 5 / 2 renders as the deterministic 2.5000.
const divisionScaleIncrement = 4

// decimalValue is an exact fixed-point number equal to unscaled * 10^-scale.
type decimalValue struct {
	unscaled *big.Int
	scale    int
}

// parseDecimalText reads a plain decimal spelling (an optional sign, an integer
// run, and an optional fractional run) into an exact fixed-point value. The
// second result is false when the spelling is empty, carries an exponent, or is
// otherwise not a plain decimal, so an approximate literal is routed elsewhere.
func parseDecimalText(text string) (decimalValue, bool) {
	negative, body, ok := splitDecimalSign(text)
	if !ok {
		return decimalValue{}, false
	}
	intPart, fracPart, ok := splitDecimalBody(body)
	if !ok {
		return decimalValue{}, false
	}
	digits := intPart + fracPart
	unscaled, valid := new(big.Int).SetString(digits, 10)
	if !valid {
		return decimalValue{}, false
	}
	if negative {
		unscaled.Neg(unscaled)
	}
	return decimalValue{unscaled: unscaled, scale: len(fracPart)}, true
}

func splitDecimalSign(text string) (bool, string, bool) {
	if text == "" {
		return false, "", false
	}
	switch text[0] {
	case '+':
		return false, text[1:], true
	case '-':
		return true, text[1:], true
	default:
		return false, text, true
	}
}

// splitDecimalBody separates the integer and fractional digit runs of an
// unsigned decimal body, rejecting an empty value and any run carrying a
// character that is not a digit. A second decimal point lands in the fractional
// run, whose digit check then rejects it.
func splitDecimalBody(body string) (string, string, bool) {
	intPart, fracPart, _ := strings.Cut(body, ".")
	if !plainDigitRun(intPart) || !plainDigitRun(fracPart) {
		return "", "", false
	}
	if intPart == "" && fracPart == "" {
		return "", "", false
	}
	if intPart == "" {
		intPart = "0"
	}
	return intPart, fracPart, true
}

// plainDigitRun reports whether a run is empty or made entirely of digits, the
// shape each side of a decimal point must take.
func plainDigitRun(run string) bool {
	return run == "" || allDigits(run)
}

func decimalFromInt(value int64) decimalValue {
	return decimalValue{unscaled: big.NewInt(value), scale: 0}
}

// rescaled returns the unscaled integer this value would carry at the target
// scale, which must be at least the current scale. Growing the scale multiplies
// by a power of ten and stays exact.
func (d decimalValue) rescaled(target int) *big.Int {
	if target == d.scale {
		return new(big.Int).Set(d.unscaled)
	}
	factor := tenToThe(target - d.scale)
	return new(big.Int).Mul(d.unscaled, factor)
}

func addDecimal(a, b decimalValue) decimalValue {
	scale := maxInt(a.scale, b.scale)
	sum := new(big.Int).Add(a.rescaled(scale), b.rescaled(scale))
	return decimalValue{unscaled: sum, scale: scale}
}

func subtractDecimal(a, b decimalValue) decimalValue {
	scale := maxInt(a.scale, b.scale)
	diff := new(big.Int).Sub(a.rescaled(scale), b.rescaled(scale))
	return decimalValue{unscaled: diff, scale: scale}
}

func multiplyDecimal(a, b decimalValue) decimalValue {
	product := new(big.Int).Mul(a.unscaled, b.unscaled)
	return decimalValue{unscaled: product, scale: a.scale + b.scale}
}

// divideDecimal divides a by b at a result scale of the dividend scale plus the
// division increment, rounding the final digit half away from zero. The second
// result is false when the divisor is zero, which the caller reports as a
// division-by-zero error rather than a silent NULL.
func divideDecimal(a, b decimalValue) (decimalValue, bool) {
	if b.unscaled.Sign() == 0 {
		return decimalValue{}, false
	}
	resultScale := a.scale + divisionScaleIncrement
	shift := resultScale + b.scale - a.scale
	numerator := new(big.Int).Mul(a.unscaled, tenToThe(shift))
	quotient := roundedQuotient(numerator, b.unscaled)
	return decimalValue{unscaled: quotient, scale: resultScale}, true
}

// roundedQuotient divides numerator by divisor and rounds the result half away
// from zero, so a remainder of exactly half a divisor rounds outward and the
// rounding direction never depends on operand sign.
func roundedQuotient(numerator, divisor *big.Int) *big.Int {
	quotient, remainder := new(big.Int).QuoRem(numerator, divisor, new(big.Int))
	if remainder.Sign() == 0 {
		return quotient
	}
	twiceRemainder := new(big.Int).Abs(remainder)
	twiceRemainder.Lsh(twiceRemainder, 1)
	if twiceRemainder.CmpAbs(divisor) < 0 {
		return quotient
	}
	return quotient.Add(quotient, big.NewInt(int64(quotientRoundSign(numerator, divisor))))
}

func quotientRoundSign(numerator, divisor *big.Int) int {
	if numerator.Sign()*divisor.Sign() < 0 {
		return -1
	}
	return 1
}

func compareDecimal(a, b decimalValue) int {
	scale := maxInt(a.scale, b.scale)
	return a.rescaled(scale).Cmp(b.rescaled(scale))
}

// roundDecimalToScale returns the value at exactly the target scale, growing the
// scale exactly or shrinking it by rounding the dropped digits half away from
// zero. It is the rescaling an explicit DECIMAL cast performs.
func roundDecimalToScale(d decimalValue, scale int) decimalValue {
	if scale >= d.scale {
		return decimalValue{unscaled: d.rescaled(scale), scale: scale}
	}
	divisor := tenToThe(d.scale - scale)
	return decimalValue{unscaled: roundedQuotient(d.unscaled, divisor), scale: scale}
}

// integerDigits reports how many digits precede the decimal point, which a
// DECIMAL cast checks against the target's integer-digit budget of precision
// minus scale.
func (d decimalValue) integerDigits() int {
	digits := len(new(big.Int).Abs(d.unscaled).String())
	if digits <= d.scale {
		return 1
	}
	return digits - d.scale
}

// toInt64 reports the value as a signed 64-bit integer when it is integral and
// within range, so a signed cast can reject an out-of-range or fractional value.
func (d decimalValue) toInt64() (int64, bool) {
	whole := roundDecimalToScale(d, 0)
	if !whole.unscaled.IsInt64() {
		return 0, false
	}
	return whole.unscaled.Int64(), true
}

// toUint64 reports the value as an unsigned 64-bit integer when it is integral,
// non-negative, and within range.
func (d decimalValue) toUint64() (uint64, bool) {
	whole := roundDecimalToScale(d, 0)
	if whole.unscaled.Sign() < 0 || !whole.unscaled.IsUint64() {
		return 0, false
	}
	return whole.unscaled.Uint64(), true
}

// withinDecimalCeiling reports whether the value's significant digit count is
// within the v0.1 DECIMAL precision ceiling. A value at or below the ceiling is
// representable; a wider value is out of range.
func (d decimalValue) withinDecimalCeiling() bool {
	return decimalSignificantDigits(d.unscaled) <= decimalPrecisionCeiling
}

// withinDecimalLimits reports whether a result decimal is within both the
// precision and the scale ceilings of the public DECIMAL contract. A literal or
// an arithmetic result whose fractional scale exceeds the ceiling fails rather
// than being silently rounded to fit.
func (d decimalValue) withinDecimalLimits() bool {
	return d.withinDecimalCeiling() && d.scale <= decimalScaleCeiling
}

func decimalSignificantDigits(unscaled *big.Int) int {
	if unscaled.Sign() == 0 {
		return 1
	}
	return len(new(big.Int).Abs(unscaled).String())
}

// renderDecimal spells the value with its full scale, keeping trailing
// fractional zeros so the rendered scale matches the computed scale. A big.Int
// is never negative zero, so a value of zero always renders without a sign.
func (d decimalValue) renderDecimal() string {
	if d.scale <= 0 {
		return d.unscaled.String()
	}
	negative := d.unscaled.Sign() < 0
	digits := new(big.Int).Abs(d.unscaled).String()
	if len(digits) <= d.scale {
		digits = strings.Repeat("0", d.scale-len(digits)+1) + digits
	}
	point := len(digits) - d.scale
	rendered := digits[:point] + "." + digits[point:]
	if negative {
		return "-" + rendered
	}
	return rendered
}

func tenToThe(power int) *big.Int {
	if power <= 0 {
		return big.NewInt(1)
	}
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
