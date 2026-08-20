// This file implements the v0.1 scalar expression engine: the value model,
// tokenizer, and the entry point that both text and prepared execution use to
// evaluate a SELECT expression that has no FROM clause. The engine follows a
// finite, documented grammar of literals, three-valued logic, arithmetic,
// explicit casts, and the closed scalar-function registry. Any spelling outside
// that grammar, an unknown function, a strict-conversion violation, and a
// runtime domain error (division by zero, overflow, a non-finite result) all
// fail with a MySQL 8.4.11 error identity. A SELECT has no durable effect, so a
// rejected expression simply returns the error rather than a partial result.
package mysql

import (
	"strconv"
	"strings"
)

// valueKind tags the numeric or character domain of an evaluated value. The
// engine keeps the exact domain rather than a single string so arithmetic and
// comparison follow MySQL's result-type rules deterministically.
type valueKind int

const (
	valueNull    valueKind = iota
	valueInt               // signed 64-bit integer
	valueUint              // unsigned 64-bit integer
	valueDecimal           // exact fixed-point decimal
	valueDouble            // approximate binary floating point
	valueString            // character string
)

// exprValue is one evaluated scalar. Only the field selected by kind is
// meaningful; a valueNull carries no payload.
type exprValue struct {
	kind valueKind
	i    int64
	u    uint64
	dec  decimalValue
	f    float64
	s    string
}

func nullValue() exprValue                    { return exprValue{kind: valueNull} }
func intValue(value int64) exprValue          { return exprValue{kind: valueInt, i: value} }
func uintValue(value uint64) exprValue        { return exprValue{kind: valueUint, u: value} }
func decimalValueOf(d decimalValue) exprValue { return exprValue{kind: valueDecimal, dec: d} }
func doubleValue(value float64) exprValue     { return exprValue{kind: valueDouble, f: value} }
func stringValue(value string) exprValue      { return exprValue{kind: valueString, s: value} }

// boolValue maps a Go boolean to the MySQL integer truth value 1 or 0. UNKNOWN
// is represented separately by a valueNull, so three-valued logic never routes
// through this helper.
func boolValue(truth bool) exprValue {
	if truth {
		return intValue(1)
	}
	return intValue(0)
}

func (v exprValue) isNull() bool { return v.kind == valueNull }

// render spells the value in the canonical text form the protocol row carries.
func (v exprValue) render() string {
	switch v.kind {
	case valueInt:
		return strconv.FormatInt(v.i, 10)
	case valueUint:
		return strconv.FormatUint(v.u, 10)
	case valueDecimal:
		return v.dec.renderDecimal()
	case valueDouble:
		return renderDouble(v.f)
	case valueString:
		return v.s
	default:
		return ""
	}
}

// renderDouble spells an approximate value with the shortest round-tripping
// decimal representation, so the same float always renders identically.
func renderDouble(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// evaluateScalar parses and evaluates a complete scalar expression. A non-nil
// error means the expression is outside the supported grammar, names an unknown
// function, violates a strict-conversion rule, or hit a runtime domain error.
func evaluateScalar(text string) (exprValue, error) {
	return evaluateScalarWithResolver(text, nil)
}

func evaluateScalarWithResolver(text string, resolve func(string) (exprValue, error)) (exprValue, error) {
	tokens, err := tokenizeExpression(text)
	if err != nil {
		return exprValue{}, err
	}
	parser := &exprParser{tokens: tokens, resolveIdentifier: resolve}
	value, err := parser.parseExpression()
	if err != nil {
		return exprValue{}, err
	}
	if !parser.atEnd() {
		return exprValue{}, unsupportedExpression()
	}
	return value, nil
}

// scalarColumn evaluates an expression for a SELECT projection and returns its
// row value, null flag, and result-column metadata. The metadata is a pure
// function of the expression text and the evaluated domain, so a text SELECT and
// a prepared SELECT of the same expression advertise identical columns.
func scalarColumn(text string) (string, bool, columnMetadata, error) {
	value, err := evaluateScalar(text)
	if err != nil {
		return "", false, columnMetadata{}, err
	}
	rendered := value.render()
	if _, unwrapped, prepared := decodePreparedTemporalLiteral(rendered); prepared {
		rendered = unwrapped
	}
	return rendered, value.isNull(), scalarMetadata(strings.TrimSpace(text), rendered, value), nil
}

// scalarMetadata builds the result-column metadata for an evaluated value. A
// numeric value advertises the binary character set; a character value
// advertises utf8mb4; a NULL advertises the NULL type. The column name is the
// verbatim expression text, matching MySQL's default column labelling.
func scalarMetadata(name, rendered string, value exprValue) columnMetadata {
	metadata := columnMetadata{catalog: "def", name: name, characterSet: mysqlCharsetBinary, flags: mysqlNotNullFlag | mysqlBinaryFlag}
	switch value.kind {
	case valueNull:
		metadata.typ, metadata.flags = mysqlTypeNull, mysqlBinaryFlag
	case valueUint:
		metadata.typ, metadata.length, metadata.flags = mysqlTypeLongLong, uint32(len(rendered)), metadata.flags|mysqlUnsignedFlag
	case valueInt:
		metadata.typ, metadata.length = mysqlTypeLongLong, uint32(len(rendered))
	case valueDecimal:
		metadata.typ, metadata.length, metadata.decimals = mysqlTypeNewDecimal, uint32(len(rendered)), byte(value.dec.scale)
	case valueDouble:
		metadata.typ, metadata.length = mysqlTypeDouble, 8
	default:
		metadata.typ, metadata.length, metadata.characterSet, metadata.flags = mysqlTypeVarString, uint32(len([]rune(rendered))*4), mysqlCharsetUTF8MB40900AICI, mysqlNotNullFlag
	}
	return metadata
}

func unsupportedExpression() error {
	return sqlFailure{1064, "42000", "unsupported expression"}
}

// expressionTooDeep reports that an expression's parenthesisation, NOT
// chain, or unary sign run exceeded maxExpressionDepth. MySQL's own error
// for this situation is ER_STACK_OVERRUN_NEED_MORE (1436); we reuse its code
// and SQLSTATE here.
func expressionTooDeep() error {
	return sqlFailure{1436, "HY000", "expression is too deeply nested"}
}
