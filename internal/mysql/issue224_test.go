package mysql

import "testing"

func TestIssue224SignedSubtractionOverflowFails(t *testing.T) {
	for _, expression := range []string{
		"-9223372036854775808 - 1",
		"(-9223372036854775807 - 1) - 1",
	} {
		_, err := evaluateScalar(expression)
		if !isFailureCode(err, 1690) {
			t.Errorf("evaluateScalar(%q) error = %v, want MySQL error 1690", expression, err)
		}
	}
}
