package mysql

import "testing"

func FuzzEvaluateScalarDoesNotPanic(f *testing.F) {
	for _, expression := range []string{
		"1 + 2",
		"'left\\x01right'",
		"((1))",
		"CAST('2024-01-01' AS DATE)",
		"NOT (NULL OR 1)",
	} {
		f.Add(expression)
	}
	f.Fuzz(func(t *testing.T, expression string) {
		_, _ = evaluateScalar(expression)
	})
}
