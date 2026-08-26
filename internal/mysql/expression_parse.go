// This file is the recursive-descent parser that both parses and evaluates a
// scalar expression in one pass. Each precedence level is a method on the parser,
// from OR at the loosest binding through the primary literals, parenthesised
// groups, casts, and function calls at the tightest; the leaf helpers are free
// functions over the parser cursor. Parsing and evaluation are fused because a
// SELECT expression has no reusable plan; the value falls out of the same descent
// that validates the grammar.
package mysql

import (
	"strconv"
	"strings"
)

type exprParser struct {
	tokens            []exprToken
	pos               int
	resolveIdentifier func(string) (exprValue, error)
	depth             int
}

// maxExpressionDepth bounds the recursion of the descent so that a
// pathologically nested expression (deep parentheses, NOT chains, unary
// minus/plus runs, or nested function calls) fails with a SQL error instead
// of exhausting the goroutine stack. A stack overflow in Go is a fatal error
// that recover() cannot catch, so without this bound a single crafted query
// takes down the whole server, not just the offending connection.
const maxExpressionDepth = 4000

// depthGuard runs fn one level deeper in the recursive descent, failing
// closed once maxExpressionDepth is exceeded instead of growing the stack
// further. Every self- or mutually-recursive entry point (parseExpression,
// parseNot, parseUnary) routes its recursive call through this single
// choke point rather than each managing its own counter.
func depthGuard(p *exprParser, fn func() (exprValue, error)) (exprValue, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxExpressionDepth {
		return exprValue{}, expressionTooDeep()
	}
	return fn()
}

func (p *exprParser) atEnd() bool { return p.pos >= len(p.tokens) }

func (p *exprParser) peek() (exprToken, bool) {
	if p.atEnd() {
		return exprToken{}, false
	}
	return p.tokens[p.pos], true
}

func (p *exprParser) advance() { p.pos++ }

func (p *exprParser) matchKeyword(word string) bool {
	token, ok := p.peek()
	if ok && token.kind == tokenIdent && strings.EqualFold(token.text, word) {
		p.advance()
		return true
	}
	return false
}

func (p *exprParser) peekOperator() (string, bool) {
	token, ok := p.peek()
	if ok && token.kind == tokenOperator {
		return token.text, true
	}
	return "", false
}

// parseExpression evaluates a full expression at the loosest OR precedence.
// It is the funnel every nested construct (parenthesised groups, function
// arguments, CAST/CONVERT targets) recurses back through, so bounding its
// depth here bounds all of them.
func (p *exprParser) parseExpression() (exprValue, error) {
	return depthGuard(p, p.parseOr)
}

func (p *exprParser) parseOr() (exprValue, error) {
	return parseLogical(p, "OR", p.parseXor, logicalOr)
}

func (p *exprParser) parseXor() (exprValue, error) {
	return parseLogical(p, "XOR", p.parseAnd, logicalXor)
}

func (p *exprParser) parseAnd() (exprValue, error) {
	return parseLogical(p, "AND", p.parseNot, logicalAnd)
}

// parseLogical folds a left-associative run of one keyword operator, combining
// operands through a three-valued-logic reducer.
func parseLogical(p *exprParser, keyword string, operand func() (exprValue, error), reduce func(exprValue, exprValue) (exprValue, error)) (exprValue, error) {
	left, err := operand()
	if err != nil {
		return exprValue{}, err
	}
	for p.matchKeyword(keyword) {
		right, rightErr := operand()
		if rightErr != nil {
			return exprValue{}, rightErr
		}
		left, err = reduce(left, right)
		if err != nil {
			return exprValue{}, err
		}
	}
	return left, nil
}

func (p *exprParser) parseNot() (exprValue, error) {
	if p.matchKeyword("NOT") {
		// NOT recurses on itself rather than through parseExpression, so a
		// long NOT NOT NOT ... chain needs its own depth guard.
		operand, err := depthGuard(p, p.parseNot)
		if err != nil {
			return exprValue{}, err
		}
		return logicalNot(operand)
	}
	return parseIs(p)
}

// parseIs applies an optional postfix IS [NOT] NULL/TRUE/FALSE/UNKNOWN test,
// which always yields a definite 1 or 0 and never itself UNKNOWN.
func parseIs(p *exprParser) (exprValue, error) {
	left, err := p.parseComparison()
	if err != nil {
		return exprValue{}, err
	}
	if !p.matchKeyword("IS") {
		return left, nil
	}
	negate := p.matchKeyword("NOT")
	return applyIsPredicate(p, left, negate)
}

func applyIsPredicate(p *exprParser, left exprValue, negate bool) (exprValue, error) {
	switch {
	case p.matchKeyword("NULL"):
		return isNullPredicate(left, negate), nil
	case p.matchKeyword("TRUE"):
		return isTruthPredicate(left, true, negate)
	case p.matchKeyword("FALSE"):
		return isTruthPredicate(left, false, negate)
	case p.matchKeyword("UNKNOWN"):
		return isUnknownPredicate(left, negate), nil
	default:
		return exprValue{}, unsupportedExpression()
	}
}

var comparisonOperators = map[string]bool{"=": true, "<>": true, "!=": true, "<=>": true, "<": true, "<=": true, ">": true, ">=": true}

// parseComparison applies a single non-associative comparison operator, matching
// MySQL's rejection of chained comparisons such as a < b < c.
func (p *exprParser) parseComparison() (exprValue, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return exprValue{}, err
	}
	if matched, negate := matchLikeOperator(p); matched {
		return parseLikeComparison(p, left, negate)
	}
	symbol, ok := p.peekOperator()
	if !ok || !comparisonOperators[symbol] {
		return left, nil
	}
	p.advance()
	right, err := p.parseAdditive()
	if err != nil {
		return exprValue{}, err
	}
	return compareValues(symbol, left, right)
}

func matchLikeOperator(p *exprParser) (bool, bool) {
	if p.matchKeyword("LIKE") {
		return true, false
	}
	token, ok := p.peek()
	if !ok || token.kind != tokenIdent || !strings.EqualFold(token.text, "NOT") {
		return false, false
	}
	if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].kind != tokenIdent || !strings.EqualFold(p.tokens[p.pos+1].text, "LIKE") {
		return false, false
	}
	p.advance()
	p.advance()
	return true, true
}

func parseLikeComparison(p *exprParser, left exprValue, negate bool) (exprValue, error) {
	right, err := p.parseAdditive()
	if err != nil {
		return exprValue{}, err
	}
	escape := "\\"
	if p.matchKeyword("ESCAPE") {
		value, escapeErr := p.parseAdditive()
		if escapeErr != nil {
			return exprValue{}, escapeErr
		}
		if value.isNull() {
			return nullValue(), nil
		}
		escape = value.render()
		if escape == "" {
			return exprValue{}, unsupportedExpression()
		}
	}
	return evalLike(left, right, escape, negate)
}

func (p *exprParser) parseAdditive() (exprValue, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return exprValue{}, err
	}
	for {
		symbol, ok := p.peekOperator()
		if !ok || (symbol != "+" && symbol != "-") {
			return left, nil
		}
		p.advance()
		right, rightErr := p.parseMultiplicative()
		if rightErr != nil {
			return exprValue{}, rightErr
		}
		left, err = arithmetic(symbol, left, right)
		if err != nil {
			return exprValue{}, err
		}
	}
}

func (p *exprParser) parseMultiplicative() (exprValue, error) {
	left, err := p.parseUnary()
	if err != nil {
		return exprValue{}, err
	}
	for {
		symbol, ok := nextMultiplicative(p)
		if !ok {
			return left, nil
		}
		right, rightErr := p.parseUnary()
		if rightErr != nil {
			return exprValue{}, rightErr
		}
		left, err = arithmetic(symbol, left, right)
		if err != nil {
			return exprValue{}, err
		}
	}
}

// nextMultiplicative consumes and normalizes the next multiplicative operator,
// treating the DIV and MOD keywords as the integer-division and modulus symbols.
func nextMultiplicative(p *exprParser) (string, bool) {
	if symbol, ok := p.peekOperator(); ok && (symbol == "*" || symbol == "/" || symbol == "%") {
		p.advance()
		return symbol, true
	}
	if p.matchKeyword("DIV") {
		return "DIV", true
	}
	if p.matchKeyword("MOD") {
		return "%", true
	}
	return "", false
}

func (p *exprParser) parseUnary() (exprValue, error) {
	symbol, ok := p.peekOperator()
	if ok && symbol == "-" {
		p.advance()
		// Unary minus recurses on itself rather than through
		// parseExpression, so a long run of minus signs needs its own
		// depth guard.
		operand, err := depthGuard(p, p.parseUnary)
		if err != nil {
			return exprValue{}, err
		}
		return negateValue(operand)
	}
	if ok && symbol == "+" {
		p.advance()
		return depthGuard(p, func() (exprValue, error) { return parseUnaryPlus(p) })
	}
	return parsePrimary(p)
}

// parseUnaryPlus enforces that unary plus applies only to a numeric operand, so
// a stray + before a string fails rather than silently passing the value.
func parseUnaryPlus(p *exprParser) (exprValue, error) {
	operand, err := p.parseUnary()
	if err != nil {
		return exprValue{}, err
	}
	if operand.isNull() || isNumericKind(operand.kind) {
		return operand, nil
	}
	return exprValue{}, strictConversionError()
}

func parsePrimary(p *exprParser) (exprValue, error) {
	token, ok := p.peek()
	if !ok {
		return exprValue{}, unsupportedExpression()
	}
	switch token.kind {
	case tokenNumber:
		p.advance()
		return numberLiteral(token.text)
	case tokenString:
		p.advance()
		return stringValue(token.str), nil
	case tokenLParen:
		return parseGroup(p)
	case tokenIdent:
		return parseIdentifierPrimary(p, token)
	default:
		return exprValue{}, unsupportedExpression()
	}
}

func parseGroup(p *exprParser) (exprValue, error) {
	p.advance()
	value, err := p.parseExpression()
	if err != nil {
		return exprValue{}, err
	}
	if !matchRParen(p) {
		return exprValue{}, unsupportedExpression()
	}
	return value, nil
}

func matchRParen(p *exprParser) bool { return matchTokenKind(p, tokenRParen) }
func matchComma(p *exprParser) bool  { return matchTokenKind(p, tokenComma) }
func matchLParen(p *exprParser) bool { return matchTokenKind(p, tokenLParen) }

func matchTokenKind(p *exprParser, kind exprTokenKind) bool {
	token, ok := p.peek()
	if ok && token.kind == kind {
		p.advance()
		return true
	}
	return false
}

// parseIdentifierPrimary resolves an identifier that begins a primary: a value
// keyword, a CAST or CONVERT form, or a function call. A bare identifier that is
// none of these has no column to bind against in a FROM-less SELECT and fails as
// an unknown column.
func parseIdentifierPrimary(p *exprParser, token exprToken) (exprValue, error) {
	if value, ok, err := keywordLiteral(p, token.text); ok {
		return value, err
	}
	if strings.EqualFold(token.text, "CAST") {
		return parseCast(p)
	}
	if strings.EqualFold(token.text, "CONVERT") {
		return parseConvert(p)
	}
	if identifierStartsCall(p) {
		return parseFunctionCall(p, token.text)
	}
	if p.resolveIdentifier != nil {
		p.advance()
		return p.resolveIdentifier(token.text)
	}
	return exprValue{}, unknownColumnError(token.text)
}

// identifierStartsCall reports whether the token following the current
// identifier is an opening parenthesis, which distinguishes a function call from
// a bare identifier.
func identifierStartsCall(p *exprParser) bool {
	return p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == tokenLParen
}

func keywordLiteral(p *exprParser, word string) (exprValue, bool, error) {
	switch strings.ToUpper(word) {
	case "NULL", "UNKNOWN":
		p.advance()
		return nullValue(), true, nil
	case "TRUE":
		p.advance()
		return intValue(1), true, nil
	case "FALSE":
		p.advance()
		return intValue(0), true, nil
	default:
		return exprValue{}, false, nil
	}
}

// parseFunctionCall consumes NAME ( args ) and dispatches to the registry. The
// identifier and the opening parenthesis are both current; arguments are parsed
// as full expressions separated by commas.
func parseFunctionCall(p *exprParser, name string) (exprValue, error) {
	p.advance() // name
	p.advance() // '('
	if strings.EqualFold(name, "SUBSTRING") {
		arguments, err := parseSubstringArgumentList(p)
		if err != nil {
			return exprValue{}, err
		}
		return callFunction(name, arguments)
	}
	arguments, err := parseArgumentList(p)
	if err != nil {
		return exprValue{}, err
	}
	return callFunction(name, arguments)
}

// parseSubstringArgumentList accepts both comma syntax and MySQL's keyword
// syntax: SUBSTRING(text, position[, length]) and
// SUBSTRING(text FROM position[ FOR length]).
func parseSubstringArgumentList(p *exprParser) ([]exprValue, error) {
	if matchRParen(p) {
		return nil, nil
	}
	text, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if p.matchKeyword("FROM") {
		return parseSubstringFromArguments(p, text)
	}
	return parseSubstringCommaArguments(p, text)
}

func parseSubstringFromArguments(p *exprParser, text exprValue) ([]exprValue, error) {
	position, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	arguments := []exprValue{text, position}
	if p.matchKeyword("FOR") {
		length, lengthErr := p.parseExpression()
		if lengthErr != nil {
			return nil, lengthErr
		}
		arguments = append(arguments, length)
	}
	if !matchRParen(p) {
		return nil, unsupportedExpression()
	}
	return arguments, nil
}

func parseSubstringCommaArguments(p *exprParser, text exprValue) ([]exprValue, error) {
	arguments := []exprValue{text}
	if matchRParen(p) {
		return arguments, nil
	}
	if !matchComma(p) {
		return nil, unsupportedExpression()
	}
	position, positionErr := p.parseExpression()
	if positionErr != nil {
		return nil, positionErr
	}
	arguments = append(arguments, position)
	if matchRParen(p) {
		return arguments, nil
	}
	if !matchComma(p) {
		return nil, unsupportedExpression()
	}
	length, lengthErr := p.parseExpression()
	if lengthErr != nil {
		return nil, lengthErr
	}
	arguments = append(arguments, length)
	if !matchRParen(p) {
		return nil, unsupportedExpression()
	}
	return arguments, nil
}

func parseArgumentList(p *exprParser) ([]exprValue, error) {
	arguments := make([]exprValue, 0, 4)
	if matchRParen(p) {
		return arguments, nil
	}
	for {
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		arguments = append(arguments, value)
		if matchRParen(p) {
			return arguments, nil
		}
		if !matchComma(p) {
			return nil, unsupportedExpression()
		}
	}
}

// numberLiteral classifies a numeric spelling into its exact or approximate
// domain: an exponent marks an approximate DOUBLE, a decimal point marks an
// exact DECIMAL, and a plain integer is a signed or unsigned integer, widening
// to DECIMAL only when it exceeds the 64-bit unsigned range.
func numberLiteral(text string) (exprValue, error) {
	if strings.ContainsAny(text, "eE") {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return exprValue{}, unsupportedExpression()
		}
		return doubleValue(value), nil
	}
	if strings.Contains(text, ".") {
		return decimalLiteral(text)
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil {
		return intValue(value), nil
	}
	if value, err := strconv.ParseUint(text, 10, 64); err == nil {
		return uintValue(value), nil
	}
	return decimalLiteral(text)
}

func decimalLiteral(text string) (exprValue, error) {
	value, ok := parseDecimalText(text)
	if !ok {
		return exprValue{}, unsupportedExpression()
	}
	if !value.withinDecimalLimits() {
		return exprValue{}, outOfRangeValue()
	}
	return decimalValueOf(value), nil
}
