package mysql

import "testing"

// Issue 272: unary minus on an unsigned value was evaluated through the
// unsigned arithmetic domain, which rejects every negation as a negative
// result instead of negating in the signed BIGINT domain. MySQL applies
// signed-range checks: an unsigned operand above MaxInt64 + 1 has no
// representable negation and fails with MySQL error 1690, while an operand
// whose negation fits the signed BIGINT range negates to a signed value.
func TestIssue272NegateUnsigned(t *testing.T) {
	value, err := evaluateScalar("-CAST(18446744073709551615 AS UNSIGNED)")
	if !isFailureCode(err, 1690) {
		t.Errorf("evaluateScalar -uint64(MaxUint64) error = %v, want MySQL error 1690", err)
	}
	if err == nil && value.render() != "-18446744073709551615" {
		t.Errorf("evaluateScalar -uint64(MaxUint64) = %q, want an out-of-range error", value.render())
	}

	for expression, want := range map[string]string{
		"-CAST(9223372036854775808 AS UNSIGNED)": "-9223372036854775808",
		"-CAST(9223372036854775807 AS UNSIGNED)": "-9223372036854775807",
		"-CAST(5 AS UNSIGNED)":                   "-5",
		"-(CAST(5 AS UNSIGNED) + 1)":             "-6",
	} {
		got, err := evaluateScalar(expression)
		if err != nil {
			t.Errorf("evaluateScalar(%q) unexpected error: %v", expression, err)
			continue
		}
		if got.kind == valueUint || got.render() != want {
			t.Errorf("evaluateScalar(%q) = %q (kind %v), want signed %q", expression, got.render(), got.kind, want)
		}
	}
}
