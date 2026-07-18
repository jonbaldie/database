package mysql

import "testing"

func TestParseNumericTypeRecognisesFamilies(t *testing.T) {
	cases := map[string]numericKind{
		"INT":           numericInteger,
		"INTEGER":       numericInteger,
		"TINYINT":       numericInteger,
		"SMALLINT":      numericInteger,
		"MEDIUMINT":     numericInteger,
		"BIGINT":        numericInteger,
		"INT UNSIGNED":  numericInteger,
		"DECIMAL(10,2)": numericDecimal,
		"NUMERIC(5)":    numericDecimal,
		"DEC(4,1)":      numericDecimal,
		"FLOAT":         numericFloat,
		"DOUBLE":        numericFloat,
		"REAL":          numericFloat,
		"BOOLEAN":       numericBoolean,
		"BOOL":          numericBoolean,
		"BIT(8)":        numericBit,
		"VARCHAR(32)":   numericNone,
		"CHAR(1)":       numericNone,
		"":              numericNone,
		"DATE":          numericNone,
	}
	for input, want := range cases {
		got, err := parseNumericType(input)
		if err != nil {
			t.Fatalf("parseNumericType(%q) unexpected error: %v", input, err)
		}
		if got.kind != want {
			t.Errorf("parseNumericType(%q).kind = %v, want %v", input, got.kind, want)
		}
	}
}

func TestParseNumericTypeRejectsCeilingViolations(t *testing.T) {
	for _, input := range []string{
		"DECIMAL(66,2)", "DECIMAL(10,31)", "DECIMAL(4,5)", "DECIMAL(0,0)",
		"BIT(0)", "BIT(65)",
	} {
		if _, err := parseNumericType(input); err == nil {
			t.Errorf("parseNumericType(%q) accepted an out-of-ceiling declaration", input)
		}
	}
}

func TestCanonicalNumericValueIntegers(t *testing.T) {
	cases := []struct {
		typeName, in, want string
	}{
		{"INT", "1", "1"},
		{"INT", "+007", "7"},
		{"INT", "-0", "0"},
		{"BIGINT", "9223372036854775807", "9223372036854775807"},
		{"INT UNSIGNED", "4294967295", "4294967295"},
		{"TINYINT", "-128", "-128"},
	}
	for _, c := range cases {
		typ, _ := parseNumericType(c.typeName)
		got, err := canonicalNumericValue(typ, c.in, "col", 1)
		if err != nil {
			t.Fatalf("canonicalNumericValue(%s,%q) error: %v", c.typeName, c.in, err)
		}
		if got != c.want {
			t.Errorf("canonicalNumericValue(%s,%q) = %q, want %q", c.typeName, c.in, got, c.want)
		}
	}
}

func TestCanonicalNumericValueIntegerFailures(t *testing.T) {
	cases := []struct {
		typeName, in string
		code         uint16
	}{
		{"TINYINT", "128", 1264},
		{"TINYINT", "-129", 1264},
		{"INT UNSIGNED", "-1", 1264},
		{"INT UNSIGNED", "4294967296", 1264},
		{"BIGINT", "9223372036854775808", 1264},
		{"INT", "abc", 1366},
		{"INT", "1.5", 1366},
		{"INT", "", 1366},
	}
	for _, c := range cases {
		typ, _ := parseNumericType(c.typeName)
		_, err := canonicalNumericValue(typ, c.in, "col", 1)
		assertSQLCode(t, err, c.code, c.typeName, c.in)
	}
}

func TestCanonicalNumericValueDecimal(t *testing.T) {
	typ, _ := parseNumericType("DECIMAL(10,2)")
	cases := map[string]string{"1": "1.00", "1.5": "1.50", "-3.4": "-3.40", "+0.10": "0.10", "12345678.99": "12345678.99"}
	for in, want := range cases {
		got, err := canonicalNumericValue(typ, in, "col", 1)
		if err != nil {
			t.Fatalf("decimal %q error: %v", in, err)
		}
		if got != want {
			t.Errorf("decimal %q = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalNumericValueDecimalFailures(t *testing.T) {
	typ, _ := parseNumericType("DECIMAL(5,2)")
	// integer part exceeds precision-scale (3 digits) -> out of range; extra fraction digit -> lossy.
	for _, in := range []string{"1234", "1.234", "x"} {
		if _, err := canonicalNumericValue(typ, in, "col", 1); err == nil {
			t.Errorf("decimal(5,2) accepted invalid value %q", in)
		}
	}
}

func TestCanonicalNumericValueFloatRejectsNonFinite(t *testing.T) {
	typ, _ := parseNumericType("DOUBLE")
	if got, err := canonicalNumericValue(typ, "1.5", "col", 1); err != nil || got != "1.5" {
		t.Fatalf("double 1.5 = %q err %v", got, err)
	}
	for _, in := range []string{"inf", "+Inf", "NaN", "abc"} {
		if _, err := canonicalNumericValue(typ, in, "col", 1); err == nil {
			t.Errorf("double accepted non-finite/invalid %q", in)
		}
	}
}

func TestCanonicalNumericValueBoolean(t *testing.T) {
	typ, _ := parseNumericType("BOOLEAN")
	cases := map[string]string{"0": "0", "1": "1", "true": "1", "FALSE": "0", "5": "5"}
	for in, want := range cases {
		got, err := canonicalNumericValue(typ, in, "col", 1)
		if err != nil {
			t.Fatalf("boolean %q error: %v", in, err)
		}
		if got != want {
			t.Errorf("boolean %q = %q, want %q", in, got, want)
		}
	}
	if _, err := canonicalNumericValue(typ, "maybe", "col", 1); err == nil {
		t.Errorf("boolean accepted non-numeric literal")
	}
}

func TestCanonicalNumericValueBit(t *testing.T) {
	typ, _ := parseNumericType("BIT(8)")
	cases := map[string]string{"0": "0", "255": "255", "b'101'": "5", "0b11": "3"}
	for in, want := range cases {
		got, err := canonicalNumericValue(typ, in, "col", 1)
		if err != nil {
			t.Fatalf("bit %q error: %v", in, err)
		}
		if got != want {
			t.Errorf("bit %q = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"256", "-1", "x"} {
		if _, err := canonicalNumericValue(typ, in, "col", 1); err == nil {
			t.Errorf("bit(8) accepted out-of-width value %q", in)
		}
	}
}

func TestCanonicalNumericValuePreservesNull(t *testing.T) {
	typ, _ := parseNumericType("INT")
	got, err := canonicalNumericValue(typ, "NULL", "col", 1)
	if err != nil || got != "NULL" {
		t.Fatalf("NULL passthrough = %q err %v", got, err)
	}
}

func TestParseNumericTypeCeilingBoundaries(t *testing.T) {
	for _, ok := range []string{"DECIMAL(65,30)", "DECIMAL(1,0)", "DECIMAL(65,0)", "BIT(1)", "BIT(64)"} {
		if _, err := parseNumericType(ok); err != nil {
			t.Errorf("parseNumericType(%q) rejected an in-ceiling declaration: %v", ok, err)
		}
	}
	if typ, _ := parseNumericType("DECIMAL"); typ.precision != 10 || typ.scale != 0 {
		t.Errorf("bare DECIMAL default = precision %d scale %d, want 10/0", typ.precision, typ.scale)
	}
	if typ, _ := parseNumericType("BIT"); typ.width != 1 {
		t.Errorf("bare BIT width = %d, want 1", typ.width)
	}
}

func TestParseNumericTypeUnsignedBounds(t *testing.T) {
	signed, _ := parseNumericType("INT")
	if signed.min != -2147483648 || signed.smax != 2147483647 || signed.unsigned {
		t.Errorf("INT bounds = %+v", signed)
	}
	unsigned, _ := parseNumericType("INT UNSIGNED")
	if unsigned.min != 0 || unsigned.umax != 4294967295 || !unsigned.unsigned {
		t.Errorf("INT UNSIGNED bounds = %+v", unsigned)
	}
}

func TestNumericWireType(t *testing.T) {
	cases := []struct {
		typeName string
		wire     byte
		length   uint32
		charset  uint16
	}{
		{"TINYINT", mysqlTypeTiny, 4, mysqlCharsetBinary},
		{"SMALLINT", mysqlTypeShort, 6, mysqlCharsetBinary},
		{"MEDIUMINT", mysqlTypeInt24, 9, mysqlCharsetBinary},
		{"INT", mysqlTypeLong, 11, mysqlCharsetBinary},
		{"BIGINT", mysqlTypeLongLong, 20, mysqlCharsetBinary},
		{"DECIMAL(10,2)", mysqlTypeNewDecimal, 12, mysqlCharsetBinary},
		{"FLOAT", mysqlTypeFloat, 12, mysqlCharsetBinary},
		{"DOUBLE", mysqlTypeDouble, 22, mysqlCharsetBinary},
		{"BOOLEAN", mysqlTypeTiny, 1, mysqlCharsetBinary},
		{"BIT(8)", mysqlTypeBit, 8, mysqlCharsetBinary},
	}
	for _, c := range cases {
		typ, _ := parseNumericType(c.typeName)
		wire, length, charset := numericWireType(typ)
		if wire != c.wire || length != c.length || charset != c.charset {
			t.Errorf("numericWireType(%s) = (%#x,%d,%d), want (%#x,%d,%d)", c.typeName, wire, length, charset, c.wire, c.length, c.charset)
		}
	}
}

func TestCanonicalDecimalScaleZero(t *testing.T) {
	typ, _ := parseNumericType("NUMERIC(5)")
	cases := map[string]string{"3": "3", "007": "7", "3.0": "3", "-0": "0"}
	for in, want := range cases {
		got, err := canonicalNumericValue(typ, in, "col", 1)
		if err != nil || got != want {
			t.Errorf("numeric(5) %q = %q err %v, want %q", in, got, err, want)
		}
	}
	if _, err := canonicalNumericValue(typ, "3.1", "col", 1); err == nil {
		t.Errorf("numeric(5) accepted lossy fraction 3.1")
	}
}

func TestCanonicalFloatWidthOverflow(t *testing.T) {
	narrow, _ := parseNumericType("FLOAT")
	if _, err := canonicalNumericValue(narrow, "1e40", "col", 1); err == nil {
		t.Errorf("FLOAT accepted a value beyond 32-bit range")
	}
	wide, _ := parseNumericType("DOUBLE")
	if got, err := canonicalNumericValue(wide, "1e40", "col", 1); err != nil || got == "" {
		t.Errorf("DOUBLE rejected an in-range wide value: %q %v", got, err)
	}
}

func TestCanonicalDecimalNegativeRetainsSign(t *testing.T) {
	typ, _ := parseNumericType("DECIMAL(6,2)")
	if got, _ := canonicalNumericValue(typ, "-12.3", "col", 1); got != "-12.30" {
		t.Errorf("negative decimal = %q, want -12.30", got)
	}
	if got, _ := canonicalNumericValue(typ, "-0.00", "col", 1); got != "0.00" {
		t.Errorf("negative zero decimal = %q, want 0.00", got)
	}
}

func assertSQLCode(t *testing.T, err error, code uint16, context ...any) {
	t.Helper()
	if err == nil {
		t.Errorf("expected error code %d, got nil (%v)", code, context)
		return
	}
	failure, ok := err.(sqlFailure)
	if !ok {
		t.Errorf("expected sqlFailure, got %T (%v)", err, context)
		return
	}
	if failure.code != code {
		t.Errorf("error code = %d, want %d (%v)", failure.code, code, context)
	}
}
