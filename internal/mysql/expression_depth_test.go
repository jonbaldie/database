package mysql

import (
	"strings"
	"testing"
)

// TestEvaluateRejectsExcessiveNesting is a regression test for a stack
// overflow: evaluateScalar's recursive-descent parser had no depth limit, so
// a query with enough nested parentheses, NOT keywords, or unary minus signs
// crashed the whole server with an unrecoverable "fatal error: stack
// overflow" (Go's stack-overflow fatal error cannot be recovered, unlike a
// panic). All three constructs share exprParser's depth counter, so each is
// exercised here. The nesting depths below are far below what fits in a
// single default-sized (64MB) packet, so this is reachable from a single
// client query.
func TestEvaluateRejectsExcessiveNesting(t *testing.T) {
	deepParens := strings.Repeat("(", 500000) + "1" + strings.Repeat(")", 500000)
	evalError(t, deepParens)

	deepNot := strings.Repeat("NOT ", 500000) + "1"
	evalError(t, deepNot)

	deepMinus := strings.Repeat("-", 500000) + "1"
	evalError(t, deepMinus)

	deepPlus := strings.Repeat("+", 500000) + "1"
	evalError(t, deepPlus)
}

// TestEvaluateAllowsModerateNesting guards against the depth limit being so
// tight that it rejects nesting a real query could plausibly contain.
func TestEvaluateAllowsModerateNesting(t *testing.T) {
	moderateParens := strings.Repeat("(", 1000) + "1" + strings.Repeat(")", 1000)
	if got := evalRender(t, moderateParens); got != "1" {
		t.Fatalf("evaluateScalar(moderate parens) = %q, want %q", got, "1")
	}

	moderateNot := strings.Repeat("NOT ", 999) + "1"
	evalRender(t, moderateNot) // must not error; result value isn't asserted.
}
