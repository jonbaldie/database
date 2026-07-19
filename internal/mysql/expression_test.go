package mysql

import "testing"

func evalRender(t *testing.T, expression string) string {
	t.Helper()
	value, err := evaluateScalar(expression)
	if err != nil {
		t.Fatalf("evaluateScalar(%q) unexpected error: %v", expression, err)
	}
	return value.render()
}

func evalError(t *testing.T, expression string) {
	t.Helper()
	if _, err := evaluateScalar(expression); err == nil {
		t.Fatalf("evaluateScalar(%q) expected an error", expression)
	}
}

func evalNull(t *testing.T, expression string) {
	t.Helper()
	value, err := evaluateScalar(expression)
	if err != nil {
		t.Fatalf("evaluateScalar(%q) unexpected error: %v", expression, err)
	}
	if !value.isNull() {
		t.Fatalf("evaluateScalar(%q) = %q, want NULL", expression, value.render())
	}
}

func TestEvaluateLiterals(t *testing.T) {
	cases := map[string]string{
		"1":                     "1",
		"  42 ":                 "42",
		"-7":                    "-7",
		"1.50":                  "1.50",
		"-1.5":                  "-1.5",
		"1e3":                   "1000",
		"1.5e0":                 "1.5",
		"'hi'":                  "hi",
		"'it''s'":               "it's",
		"TRUE":                  "1",
		"FALSE":                 "0",
		"18446744073709551615":  "18446744073709551615",
		"999999999999999999999": "999999999999999999999",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
	evalNull(t, "NULL")
	evalNull(t, "UNKNOWN")
}

func TestEvaluateArithmetic(t *testing.T) {
	cases := map[string]string{
		"1 + 2":       "3",
		"2 - 5":       "-3",
		"3 * 4":       "12",
		"7 / 2":       "3.5000",
		"0.1 + 0.2":   "0.3",
		"2 + 3 * 4":   "14",
		"(2 + 3) * 4": "20",
		"7 DIV 2":     "3",
		"-7 DIV 2":    "-3",
		"5.5 DIV 2":   "2",
		"7.5 DIV 2":   "3",
		"5.5e0 DIV 2": "2",
		"7 % 3":       "1",
		"-7 % 3":      "-1",
		"5.5 % 2":     "1.5",
		"-5.5 % 2":    "-1.5",
		"5.5e0 % 2":   "1.5",
		"1.5e0 + 1":   "2.5",
		"10 / 4":      "2.5000",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
}

func TestEvaluateArithmeticFailsClosed(t *testing.T) {
	for _, expression := range []string{
		"9223372036854775807 + 1",
		"-9223372036854775807 - 2",
		"9223372036854775807 * 2",
		"1 / 0",
		"1 DIV 0",
		"5 % 0",
		"5.5 DIV 0",
		"5.5 % 0",
		"5.5e0 % 0",
		"'a' + 1",
		"1 + 'a'",
		"'a' DIV 1",
		"1e308 * 1e308",
		"0.00000000000000000000000000000001",
	} {
		evalError(t, expression)
	}
}

func TestEvaluateComparison(t *testing.T) {
	cases := map[string]string{
		"1 = 1":         "1",
		"1 = 2":         "0",
		"1 <> 2":        "1",
		"1 != 1":        "0",
		"2 > 1":         "1",
		"1 < 2":         "1",
		"1 <= 1":        "1",
		"2 >= 3":        "0",
		"1.0 = 1":       "1",
		"1 < 2.5":       "1",
		"'abc' = 'ABC'": "1",
		"'abc' = 'abd'": "0",
		"'abc' < 'abd'": "1",
		"NULL <=> NULL": "1",
		"1 <=> NULL":    "0",
		"1 <=> 1":       "1",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
	evalNull(t, "1 = NULL")
	evalNull(t, "NULL < 2")
	evalError(t, "'a' = 1")
	evalError(t, "1 < 2 < 3")
}

func TestEvaluateThreeValuedLogic(t *testing.T) {
	cases := map[string]string{
		"1 AND 1":    "1",
		"1 AND 0":    "0",
		"0 AND NULL": "0",
		"0 OR 0":     "0",
		"1 OR NULL":  "1",
		"1 XOR 0":    "1",
		"1 XOR 1":    "0",
		"NOT 1":      "0",
		"NOT 0":      "1",
		"NOT 1 = 1":  "0",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
	evalNull(t, "1 AND NULL")
	evalNull(t, "NULL AND NULL")
	evalNull(t, "0 OR NULL")
	evalNull(t, "1 XOR NULL")
	evalNull(t, "NOT NULL")
	evalError(t, "'x' AND 1")
}

func TestEvaluateIsPredicates(t *testing.T) {
	cases := map[string]string{
		"NULL IS NULL":        "1",
		"1 IS NULL":           "0",
		"1 IS NOT NULL":       "1",
		"1 IS TRUE":           "1",
		"0 IS TRUE":           "0",
		"NULL IS TRUE":        "0",
		"NULL IS NOT TRUE":    "1",
		"0 IS FALSE":          "1",
		"1 IS FALSE":          "0",
		"NULL IS UNKNOWN":     "1",
		"1 IS UNKNOWN":        "0",
		"NULL IS NOT UNKNOWN": "0",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
	evalError(t, "'x' IS TRUE")
	evalError(t, "1 IS SOMETHING")
}

func TestEvaluateCasts(t *testing.T) {
	cases := map[string]string{
		"CAST(1 AS DECIMAL(10,2))":     "1.00",
		"CAST('42' AS SIGNED)":         "42",
		"CAST(3.7 AS SIGNED)":          "4",
		"CAST(2.5 AS SIGNED)":          "3",
		"CAST(-2.5 AS SIGNED)":         "-3",
		"CAST(2.567 AS DECIMAL(10,2))": "2.57",
		"CAST(255 AS CHAR)":            "255",
		"CAST(255 AS CHAR(3))":         "255",
		"CAST('7' AS UNSIGNED)":        "7",
		"CONVERT(1, DECIMAL(4,1))":     "1.0",
		"CAST(1 AS SIGNED INTEGER)":    "1",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
	evalNull(t, "CAST(NULL AS SIGNED)")
	for _, expression := range []string{
		"CAST(-1 AS UNSIGNED)",
		"CAST('abc' AS SIGNED)",
		"CAST(255 AS CHAR(2))",
		"CAST(1 AS FOO)",
		"CAST(1 AS DECIMAL(2,3))",
		"CAST(100 AS DECIMAL(2,0))",
	} {
		evalError(t, expression)
	}
}

func TestEvaluateRejectsMalformedExpressions(t *testing.T) {
	for _, expression := range []string{
		"",
		"1 +",
		"(1",
		"1)",
		"foo",
		"1 2",
		"& 1",
		"'unterminated",
		"1.2.3",
	} {
		evalError(t, expression)
	}
}
