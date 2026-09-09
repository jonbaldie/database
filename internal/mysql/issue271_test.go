package mysql

import "testing"

// Issue 271: mulInt64 evaluated its overflow guard as product/b != a before
// the MinInt64 * -1 special case. For a = MinInt64 and b = -1 that guard
// division is itself MinInt64 / -1, which overflows, so the function's
// correctness must not depend on the toolchain compiling it without a
// hardware trap. integerModulo must keep returning 0 for a divisor of -1,
// matching MySQL, instead of relying on native MinInt64 % -1 behavior.
func TestIssue271MinInt64MultiplyAndModulo(t *testing.T) {
	value, err := evaluateScalar("(-9223372036854775807 - 1) * -1")
	if !isFailureCode(err, 1690) {
		t.Errorf("evaluateScalar MinInt64 * -1 error = %v, want MySQL error 1690", err)
	}
	if err == nil && value.render() != "9223372036854775808" {
		t.Errorf("evaluateScalar MinInt64 * -1 = %q, want an out-of-range error", value.render())
	}

	got, err := evaluateScalar("(-9223372036854775807 - 1) % -1")
	if err != nil {
		t.Errorf("evaluateScalar MinInt64 %% -1 unexpected error: %v", err)
	} else if got.render() != "0" {
		t.Errorf("evaluateScalar MinInt64 %% -1 = %q, want 0", got.render())
	}

	remainder, err := evaluateScalar("(-9223372036854775807 - 1) % 3")
	if err != nil {
		t.Errorf("evaluateScalar MinInt64 %% 3 unexpected error: %v", err)
	} else if remainder.render() != "-2" {
		t.Errorf("evaluateScalar MinInt64 %% 3 = %q, want -2", remainder.render())
	}
}
