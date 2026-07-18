// This file implements the v0.1 strict numeric and bit value contract: the
// supported integer, decimal, floating-point, Boolean, and bit families with
// range, precision, finiteness, and lossy-conversion enforcement. Malformed or
// out-of-range values fail with MySQL 8.4.11 error identity before any durable
// effect, so a rejected write never changes a table.
package mysql

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type numericKind int

const (
	numericNone numericKind = iota
	numericInteger
	numericDecimal
	numericFloat
	numericBoolean
	numericBit
)

// numericType is the parsed description of a declared numeric or bit column.
type numericType struct {
	kind      numericKind
	min       int64
	smax      int64
	umax      uint64
	unsigned  bool
	precision int
	scale     int
	width     int
	sixtyFour bool
	wire      byte
	wireLen   uint32
}

type integerBounds struct {
	min    int64
	max    uint64
	wire   byte
	length uint32
}

var integerFamilies = map[string]integerBounds{
	"TINYINT":   {min: -128, max: 255, wire: mysqlTypeTiny, length: 4},
	"SMALLINT":  {min: -32768, max: 65535, wire: mysqlTypeShort, length: 6},
	"MEDIUMINT": {min: -8388608, max: 16777215, wire: mysqlTypeInt24, length: 9},
	"INT":       {min: -2147483648, max: 4294967295, wire: mysqlTypeLong, length: 11},
	"INTEGER":   {min: -2147483648, max: 4294967295, wire: mysqlTypeLong, length: 11},
	"BIGINT":    {min: -9223372036854775808, max: 18446744073709551615, wire: mysqlTypeLongLong, length: 20},
}

// parseNumericType parses a declared column type. A numericNone kind means the
// declaration is not a numeric or bit family and belongs to another contract.
// A non-nil error means the declaration is numeric but violates a public
// ceiling and must be rejected.
func parseNumericType(typeName string) (numericType, error) {
	base, argument, unsigned := splitNumericType(typeName)
	if bounds, ok := integerFamilies[base]; ok {
		return integerNumericType(bounds, unsigned), nil
	}
	switch base {
	case "DECIMAL", "NUMERIC", "DEC", "FIXED":
		return decimalNumericType(argument, unsigned)
	case "FLOAT":
		return numericType{kind: numericFloat, unsigned: unsigned}, nil
	case "DOUBLE", "REAL":
		return numericType{kind: numericFloat, unsigned: unsigned, sixtyFour: true}, nil
	case "BOOL", "BOOLEAN":
		typ := integerNumericType(integerFamilies["TINYINT"], false)
		typ.kind = numericBoolean
		return typ, nil
	case "BIT":
		return bitNumericType(argument)
	default:
		return numericType{}, nil
	}
}

// splitNumericType separates an upper-cased base name, an optional parenthesised
// argument, and a trailing UNSIGNED modifier from a declared type token.
func splitNumericType(typeName string) (string, string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(typeName))
	unsigned := false
	if trimmed, ok := strings.CutSuffix(normalized, " UNSIGNED"); ok {
		normalized, unsigned = strings.TrimSpace(trimmed), true
	}
	open := strings.IndexByte(normalized, '(')
	if open < 0 {
		return normalized, "", unsigned
	}
	end := strings.LastIndexByte(normalized, ')')
	if end <= open {
		return normalized, "", unsigned
	}
	return strings.TrimSpace(normalized[:open]), normalized[open+1 : end], unsigned
}

func integerNumericType(bounds integerBounds, unsigned bool) numericType {
	typ := numericType{kind: numericInteger, unsigned: unsigned, min: bounds.min, smax: int64((bounds.max - 1) / 2), umax: bounds.max, wire: bounds.wire, wireLen: bounds.length}
	if unsigned {
		typ.min = 0
	}
	return typ
}

// numericWireType maps a parsed numeric or bit type to its MySQL result column
// wire type, display length, and character set so metadata matches the contract.
func numericWireType(typ numericType) (byte, uint32, uint16) {
	switch typ.kind {
	case numericDecimal:
		return mysqlTypeNewDecimal, uint32(typ.precision + 2), mysqlCharsetBinary
	case numericFloat:
		if typ.sixtyFour {
			return mysqlTypeDouble, 22, mysqlCharsetBinary
		}
		return mysqlTypeFloat, 12, mysqlCharsetBinary
	case numericBoolean:
		return mysqlTypeTiny, 1, mysqlCharsetBinary
	case numericBit:
		return mysqlTypeBit, uint32(typ.width), mysqlCharsetBinary
	default:
		return typ.wire, typ.wireLen, mysqlCharsetBinary
	}
}

func decimalNumericType(argument string, unsigned bool) (numericType, error) {
	precision, scale, err := decimalPrecisionScale(argument)
	if err != nil {
		return numericType{}, err
	}
	return numericType{kind: numericDecimal, unsigned: unsigned, precision: precision, scale: scale}, nil
}

func decimalPrecisionScale(argument string) (int, int, error) {
	precision, scale := 10, 0
	if argument != "" {
		parts := strings.SplitN(argument, ",", 2)
		var err error
		if precision, err = strconv.Atoi(strings.TrimSpace(parts[0])); err != nil {
			return 0, 0, sqlFailure{1064, "42000", "invalid DECIMAL precision"}
		}
		if len(parts) == 2 {
			if scale, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil {
				return 0, 0, sqlFailure{1064, "42000", "invalid DECIMAL scale"}
			}
		}
	}
	return precision, scale, validateDecimalCeiling(precision, scale)
}

func validateDecimalCeiling(precision, scale int) error {
	if precision < 1 || precision > 65 {
		return sqlFailure{1426, "42000", fmt.Sprintf("Too big precision %d specified. Maximum is 65", precision)}
	}
	if scale < 0 || scale > 30 {
		return sqlFailure{1425, "42000", fmt.Sprintf("Too big scale %d specified. Maximum is 30", scale)}
	}
	if scale > precision {
		return sqlFailure{1427, "42000", "scale must not be larger than precision"}
	}
	return nil
}

func bitNumericType(argument string) (numericType, error) {
	width := 1
	if argument != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(argument))
		if err != nil {
			return numericType{}, sqlFailure{1064, "42000", "invalid BIT width"}
		}
		width = parsed
	}
	if width < 1 || width > 64 {
		return numericType{}, sqlFailure{1439, "42000", fmt.Sprintf("Display width out of range for BIT column (max = 64), got %d", width)}
	}
	return numericType{kind: numericBit, width: width}, nil
}

// canonicalNumericValue validates a supplied literal against a numeric column
// type and returns its canonical stored representation. A NULL literal passes
// through unchanged. Any malformed, out-of-range, non-finite, or lossy value is
// rejected with MySQL error identity so the caller can fail before durability.
func canonicalNumericValue(typ numericType, raw, column string, row int) (string, error) {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "null") {
		return "NULL", nil
	}
	switch typ.kind {
	case numericInteger:
		return canonicalIntegerValue(typ, value, column, row)
	case numericBoolean:
		return canonicalBooleanValue(typ, value, column, row)
	case numericDecimal:
		return canonicalDecimalValue(typ, value, column, row)
	case numericFloat:
		return canonicalFloatValue(typ, value, column, row)
	case numericBit:
		return canonicalBitValue(typ, value, column, row)
	default:
		return value, nil
	}
}

func canonicalBooleanValue(typ numericType, value, column string, row int) (string, error) {
	if strings.EqualFold(value, "true") {
		return "1", nil
	}
	if strings.EqualFold(value, "false") {
		return "0", nil
	}
	return canonicalIntegerValue(typ, value, column, row)
}

func canonicalIntegerValue(typ numericType, value, column string, row int) (string, error) {
	if typ.unsigned {
		return canonicalUnsignedValue(typ, value, column, row)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return "", integerFailure(err, column, value, row)
	}
	if parsed < typ.min || parsed > typ.smax {
		return "", outOfRange(column, row)
	}
	return strconv.FormatInt(parsed, 10), nil
}

func canonicalUnsignedValue(typ numericType, value, column string, row int) (string, error) {
	if signed, err := strconv.ParseInt(value, 10, 64); err == nil && signed < 0 {
		return "", outOfRange(column, row)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return "", integerFailure(err, column, value, row)
	}
	if parsed > typ.umax {
		return "", outOfRange(column, row)
	}
	return strconv.FormatUint(parsed, 10), nil
}

func integerFailure(err error, column, value string, row int) error {
	if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
		return outOfRange(column, row)
	}
	return incorrectValue("integer", column, value, row)
}

func canonicalDecimalValue(typ numericType, value, column string, row int) (string, error) {
	negative, intPart, fracPart, ok := splitDecimal(value)
	if !ok {
		return "", incorrectValue("decimal", column, value, row)
	}
	intDigits := strings.TrimLeft(intPart, "0")
	if len(intDigits) > typ.precision-typ.scale {
		return "", outOfRange(column, row)
	}
	fraction, ok := scaledFraction(fracPart, typ.scale)
	if !ok {
		return "", incorrectValue("decimal", column, value, row)
	}
	return assembleDecimal(negative, intDigits, fraction, typ.scale), nil
}

// splitDecimal validates a plain decimal literal and returns its sign and the
// integer and fractional digit runs. Exponent notation is not a DECIMAL literal.
func splitDecimal(value string) (bool, string, string, bool) {
	negative := strings.HasPrefix(value, "-")
	digits := strings.TrimPrefix(strings.TrimPrefix(value, "-"), "+")
	intPart, fracPart := digits, ""
	if before, after, found := strings.Cut(digits, "."); found {
		intPart, fracPart = before, after
	}
	if intPart == "" && fracPart == "" {
		return false, "", "", false
	}
	if !allDigits(intPart) || !allDigits(fracPart) {
		return false, "", "", false
	}
	return negative, intPart, fracPart, true
}

func allDigits(run string) bool {
	for _, character := range run {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// scaledFraction fits a fractional run to the declared scale. Fewer digits pad
// with zeros; extra digits are rejected as lossy unless they are all zero.
func scaledFraction(fracPart string, scale int) (string, bool) {
	if len(fracPart) > scale {
		if strings.Trim(fracPart[scale:], "0") != "" {
			return "", false
		}
		fracPart = fracPart[:scale]
	}
	return fracPart + strings.Repeat("0", scale-len(fracPart)), true
}

func assembleDecimal(negative bool, intDigits, fraction string, scale int) string {
	if intDigits == "" {
		intDigits = "0"
	}
	result := intDigits
	if scale > 0 {
		result += "." + fraction
	}
	if negative && strings.Trim(intDigits+fraction, "0") != "" {
		return "-" + result
	}
	return result
}

func canonicalFloatValue(typ numericType, value, column string, row int) (string, error) {
	bits := 32
	if typ.sixtyFour {
		bits = 64
	}
	parsed, err := strconv.ParseFloat(value, bits)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return "", incorrectValue("double", column, value, row)
	}
	return strconv.FormatFloat(parsed, 'g', -1, bits), nil
}

func canonicalBitValue(typ numericType, value, column string, row int) (string, error) {
	parsed, err := parseBitLiteral(value)
	if err != nil {
		return "", incorrectValue("bit", column, value, row)
	}
	if typ.width < 64 && parsed >= uint64(1)<<uint(typ.width) {
		return "", outOfRange(column, row)
	}
	return strconv.FormatUint(parsed, 10), nil
}

func parseBitLiteral(value string) (uint64, error) {
	lower := strings.ToLower(value)
	if binary, ok := trimBinaryLiteral(lower); ok {
		return strconv.ParseUint(binary, 2, 64)
	}
	return strconv.ParseUint(value, 10, 64)
}

func trimBinaryLiteral(lower string) (string, bool) {
	if strings.HasPrefix(lower, "b'") && strings.HasSuffix(lower, "'") {
		return lower[2 : len(lower)-1], true
	}
	if strings.HasPrefix(lower, "0b") {
		return lower[2:], true
	}
	return "", false
}

func outOfRange(column string, row int) error {
	return sqlFailure{1264, "22003", fmt.Sprintf("Out of range value for column '%s' at row %d", column, row)}
}

func incorrectValue(kind, column, value string, row int) error {
	return sqlFailure{1366, "HY000", fmt.Sprintf("Incorrect %s value: '%s' for column '%s' at row %d", kind, value, column, row)}
}
