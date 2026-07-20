package mysql

import (
	"strings"
	"testing"
)

func mustDecimal(t *testing.T, text string) decimalValue {
	t.Helper()
	value, ok := parseDecimalText(text)
	if !ok {
		t.Fatalf("parseDecimalText(%q) rejected a valid decimal", text)
	}
	return value
}

func TestParseDecimalTextRoundTrips(t *testing.T) {
	cases := map[string]string{
		"0":       "0",
		"12":      "12",
		"+12":     "12",
		"-7":      "-7",
		"0.10":    "0.10",
		"-0.005":  "-0.005",
		".5":      "0.5",
		"12.":     "12",
		"000.500": "0.500",
	}
	for in, want := range cases {
		if got := mustDecimal(t, in).renderDecimal(); got != want {
			t.Errorf("parse/render(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDecimalTextRejectsNonDecimals(t *testing.T) {
	for _, in := range []string{"", "+", "-", ".", "1.2.3", "1e5", "0x1", "1 2", "abc", "1,2"} {
		if _, ok := parseDecimalText(in); ok {
			t.Errorf("parseDecimalText(%q) accepted a non-decimal", in)
		}
	}
}

func TestDecimalArithmeticIsExact(t *testing.T) {
	sum := addDecimal(mustDecimal(t, "0.1"), mustDecimal(t, "0.2"))
	if got := sum.renderDecimal(); got != "0.3" {
		t.Errorf("0.1 + 0.2 = %q, want 0.3", got)
	}
	diff := subtractDecimal(mustDecimal(t, "1.00"), mustDecimal(t, "0.1"))
	if got := diff.renderDecimal(); got != "0.90" {
		t.Errorf("1.00 - 0.1 = %q, want 0.90", got)
	}
	product := multiplyDecimal(mustDecimal(t, "1.5"), mustDecimal(t, "2.5"))
	if got := product.renderDecimal(); got != "3.75" {
		t.Errorf("1.5 * 2.5 = %q, want 3.75", got)
	}
}

func TestDivideDecimalRoundsHalfAwayFromZero(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"5", "2", "2.5000"},
		{"1", "3", "0.3333"},
		{"2", "3", "0.6667"},
		{"-2", "3", "-0.6667"},
		{"10", "4", "2.5000"},
	}
	for _, c := range cases {
		quotient, ok := divideDecimal(mustDecimal(t, c.a), mustDecimal(t, c.b))
		if !ok {
			t.Fatalf("divideDecimal(%s,%s) reported divide by zero", c.a, c.b)
		}
		if got := quotient.renderDecimal(); got != c.want {
			t.Errorf("%s / %s = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestDivideDecimalByZeroFails(t *testing.T) {
	if _, ok := divideDecimal(mustDecimal(t, "1"), mustDecimal(t, "0.0")); ok {
		t.Fatalf("divideDecimal by zero was accepted")
	}
}

func TestCompareDecimalOrdersAcrossScales(t *testing.T) {
	if compareDecimal(mustDecimal(t, "1.0"), mustDecimal(t, "1.00")) != 0 {
		t.Errorf("1.0 and 1.00 compared unequal")
	}
	if compareDecimal(mustDecimal(t, "1.2"), mustDecimal(t, "1.19")) <= 0 {
		t.Errorf("1.2 did not order above 1.19")
	}
	if compareDecimal(mustDecimal(t, "-1"), mustDecimal(t, "0")) >= 0 {
		t.Errorf("-1 did not order below 0")
	}
}

func TestDecimalCeilingRejectsExcessDigits(t *testing.T) {
	if !mustDecimal(t, strings.Repeat("9", decimalPrecisionCeiling)).withinDecimalCeiling() {
		t.Errorf("a value at the ceiling was rejected")
	}
	if mustDecimal(t, strings.Repeat("9", decimalPrecisionCeiling+1)).withinDecimalCeiling() {
		t.Errorf("a value past the ceiling was accepted")
	}
	if !decimalFromInt(0).withinDecimalCeiling() {
		t.Errorf("zero was rejected by the ceiling")
	}
}

func TestDecimalFromIntRenders(t *testing.T) {
	if got := decimalFromInt(-42).renderDecimal(); got != "-42" {
		t.Errorf("decimalFromInt(-42) = %q, want -42", got)
	}
}
