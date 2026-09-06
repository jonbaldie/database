package mysql

import (
	"strings"
	"testing"
)

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
		"1 + 2":                        "3",
		"2 - 5":                        "-3",
		"3 * 4":                        "12",
		"7 / 2":                        "3.5000",
		"0.1 + 0.2":                    "0.3",
		"2 + 3 * 4":                    "14",
		"(2 + 3) * 4":                  "20",
		"7 DIV 2":                      "3",
		"-7 DIV 2":                     "-3",
		"5.5 DIV 2":                    "2",
		"7.5 DIV 2":                    "3",
		"5.5e0 DIV 2":                  "2",
		"9223372036854775807 DIV 1e0":  "9223372036854775807",
		"9223372036854775807 DIV 2e0":  "4611686018427387904",
		"-9223372036854775807 DIV 1e0": "-9223372036854775808",
		"-9223372036854775807 DIV 2e0": "-4611686018427387904",
		"7 % 3":                        "1",
		"-7 % 3":                       "-1",
		"5.5 % 2":                      "1.5",
		"-5.5 % 2":                     "-1.5",
		"5.5e0 % 2":                    "1.5",
		"1.5e0 + 1":                    "2.5",
		"10 / 4":                       "2.5000",
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
		"9223372036854775807 DIV 0e0",
		"1e19 DIV 1e0",
		"'a' + 1",
		"1 + 'a'",
		"'a' DIV 1",
		"1e308 * 1e308",
		"0.00000000000000000000000000000001",
	} {
		evalError(t, expression)
	}
}

func TestEvaluateUnsignedArithmeticRejectsOutOfRange(t *testing.T) {
	for _, expression := range []string{
		"CAST(1 AS UNSIGNED) - 2",
		"5 - CAST(10 AS UNSIGNED)",
		"-CAST(1 AS UNSIGNED)",
		"CAST(18446744073709551615 AS UNSIGNED) + 1",
		"(CAST(10 AS UNSIGNED) DIV 2) - 10",
		"CAST(10 AS UNSIGNED) DIV -2",
		"-10 DIV CAST(2 AS UNSIGNED)",
		"(CAST(10 AS UNSIGNED) % 3) - 10",
		"(18446744073709551615 % 10) - 10",
	} {
		if _, err := evaluateScalar(expression); !isFailureCode(err, 1690) {
			t.Errorf("evaluateScalar(%q) error = %v, want MySQL error 1690", expression, err)
		}
	}
}

func TestEvaluateUnsignedArithmeticKeepsUnsignedDomain(t *testing.T) {
	for expression, want := range map[string]uint64{
		"CAST(1 AS UNSIGNED) + 2":                     3,
		"CAST(18446744073709551615 AS UNSIGNED) - 1":  18446744073709551614,
		"CAST(3 AS UNSIGNED) * CAST(4 AS UNSIGNED)":   12,
		"18446744073709551615 DIV 1":                  18446744073709551615,
		"CAST(9223372036854775808 AS UNSIGNED) DIV 1": 9223372036854775808,
		"CAST(10 AS UNSIGNED) DIV 2":                  5,
		"18446744073709551615 % 10":                   5,
		"CAST(10 AS UNSIGNED) % 3":                    1,
	} {
		value, err := evaluateScalar(expression)
		if err != nil {
			t.Fatalf("evaluateScalar(%q) unexpected error: %v", expression, err)
		}
		if value.kind != valueUint || value.u != want {
			t.Errorf("evaluateScalar(%q) = %#v, want unsigned %d", expression, value, want)
		}
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

func TestEvaluateExtendedFunctionSemantics(t *testing.T) {
	cases := map[string]string{
		"ROUND(2.5e0)":                      "2",
		"ROUND(-2.5e0)":                     "-2",
		"ROUND(25e0, -1)":                   "20",
		"SUBSTRING('abcdef' FROM 2 FOR 3)":  "bcd",
		"SUBSTRING('abcdef' FROM -2 FOR 1)": "e",
		"SUBSTRING('abcdef' FROM 3)":        "cdef",
		"SUBSTRING('abcdef', -7)":           "",
		"SUBSTRING('abcdef', -6)":           "abcdef",
		"SUBSTRING('abcdef', -100)":         "",
		"SUBSTRING('abcdef', 0)":            "",
		"SUBSTRING('', -1)":                 "",
		"SUBSTRING('', 0)":                  "",
		"SUBSTRING('', 1)":                  "",
		"LOCATE('B', 'abc')":                "2",
		"LOCATE('', '')":                    "1",
		"LOCATE('', '', 1)":                 "1",
		"LOCATE('', '', 2)":                 "0",
		"LOCATE('', 'abc', 1)":              "1",
		"LOCATE('', 'abc', 3)":              "3",
		"LOCATE('', 'abc', 4)":              "4",
		"LOCATE('', 'abc', 5)":              "0",
		"LOCATE('', 'abc', 0)":              "0",
		"LOCATE('a', '')":                   "0",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
	for _, expression := range []string{"POW(2, 3)", "SUBSTR('abc', 1)"} {
		evalError(t, expression)
	}
}

func TestExtendedFunctionsRejectInvalidBoundsAndCeilings(t *testing.T) {
	for _, expression := range []string{
		"SUBSTRING('abc', 99, 'x')",
		"ROUND(1.2, 31)",
		"ROUND(1.2, -31)",
	} {
		evalError(t, expression)
	}

	input := strings.Repeat("a", characterScalarCeiling/2+1)
	_, err := replaceValue([]exprValue{stringValue(input), stringValue("a"), stringValue("aa")})
	if !isFailureCode(err, 1406) {
		t.Fatalf("REPLACE expanded scalar was not rejected: %v", err)
	}
}

func TestEvaluateLike(t *testing.T) {
	cases := map[string]string{
		"'abc' LIKE 'abc'":           "1",
		"'abc' LIKE 'a%'":            "1",
		"'abc' LIKE '%c'":            "1",
		"'abc' LIKE 'a_c'":           "1",
		"'abc' LIKE 'A%'":            "1",
		"'abc' LIKE 'x%'":            "0",
		"'a%c' LIKE 'a\\%c'":         "1",
		"'abc' NOT LIKE 'x%'":        "1",
		"'abc' LIKE 'a%' ESCAPE '#'": "1",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
	evalNull(t, "NULL LIKE 'a%'")
	evalNull(t, "'abc' LIKE NULL")
}
