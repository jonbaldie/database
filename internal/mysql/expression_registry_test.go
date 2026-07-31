package mysql

import "testing"

func TestScalarFunctionsProduceDocumentedResults(t *testing.T) {
	cases := map[string]string{
		"ABS(-5)":               "5",
		"ABS(-5.5)":             "5.5",
		"ABS(2.0)":              "2.0",
		"CEILING(1.2)":          "2",
		"CEIL(-1.2)":            "-1",
		"FLOOR(1.8)":            "1",
		"FLOOR(-1.2)":           "-2",
		"SIGN(-3)":              "-1",
		"SIGN(0)":               "0",
		"SIGN(3)":               "1",
		"MOD(7, 3)":             "1",
		"MOD(5.5, 2)":           "1.5",
		"LENGTH('abc')":         "3",
		"CHAR_LENGTH('abc')":    "3",
		"CHARACTER_LENGTH('a')": "1",
		"UPPER('aB')":           "AB",
		"LCASE('aB')":           "ab",
		"CONCAT('a', 'b', 'c')": "abc",
		"COALESCE(NULL, 'x')":   "x",
		"IFNULL(NULL, 5)":       "5",
		"IFNULL(3, 5)":          "3",
		"NULLIF(1, 2)":          "1",
		"IF(1, 'y', 'n')":       "y",
		"IF(0, 'y', 'n')":       "n",
		"IF(NULL, 'y', 'n')":    "n",
		"GREATEST(1, 5, 3)":     "5",
		"LEAST(4, 2, 9)":        "2",
		"TRIM('  hi  ')":        "hi",
		"LTRIM('  hi')":         "hi",
		"RTRIM('hi  ')":         "hi",
		"REVERSE('abc')":        "cba",
	}
	for expression, want := range cases {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
}

func TestScalarFunctionsPropagateNullAndFailClosed(t *testing.T) {
	evalNull(t, "CONCAT('a', NULL)")
	evalNull(t, "COALESCE(NULL, NULL)")
	evalNull(t, "NULLIF(1, 1)")
	evalNull(t, "GREATEST(1, NULL)")
	evalNull(t, "ABS(NULL)")
	for _, expression := range []string{
		"FOO(1)",
		"ABS(1, 2)",
		"ABS()",
		"CONCAT('a', 1)",
		"LENGTH(1)",
		"GREATEST('a', 1)",
	} {
		evalError(t, expression)
	}
}

func TestFunctionSignaturesAreDiscoverableAndStable(t *testing.T) {
	signatures := functionSignatures()
	if len(signatures) != len(scalarFunctions) {
		t.Fatalf("functionSignatures returned %d entries, want %d", len(signatures), len(scalarFunctions))
	}
	for index := 1; index < len(signatures); index++ {
		if signatures[index-1].name >= signatures[index].name {
			t.Fatalf("signatures not sorted: %q before %q", signatures[index-1].name, signatures[index].name)
		}
	}
	byName := map[string]functionSignature{}
	for _, signature := range signatures {
		byName[signature.name] = signature
	}
	if abs := byName["ABS"]; abs.minArgs != 1 || abs.maxArgs != 1 {
		t.Errorf("ABS signature = %+v, want arity 1..1", abs)
	}
	if concat := byName["CONCAT"]; concat.minArgs != 1 || concat.maxArgs != variadicArity {
		t.Errorf("CONCAT signature = %+v, want 1..variadic", concat)
	}
	for _, name := range []string{"LOCATE", "POWER", "REPLACE", "ROUND", "SQRT", "SUBSTRING"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("function registry missing %s", name)
		}
	}
}

func TestScalarColumnMetadataMatchesDomain(t *testing.T) {
	cases := []struct {
		expression string
		typ        byte
		charset    uint16
		unsigned   bool
	}{
		{"7", mysqlTypeLongLong, mysqlCharsetBinary, false},
		{"18446744073709551615", mysqlTypeLongLong, mysqlCharsetBinary, true},
		{"1.50", mysqlTypeNewDecimal, mysqlCharsetBinary, false},
		{"1.5e0", mysqlTypeDouble, mysqlCharsetBinary, false},
		{"'hi'", mysqlTypeVarString, mysqlCharsetUTF8MB40900AICI, false},
		{"NULL", mysqlTypeNull, mysqlCharsetBinary, false},
	}
	for _, c := range cases {
		_, _, metadata, err := scalarColumn(c.expression)
		if err != nil {
			t.Fatalf("scalarColumn(%q) error: %v", c.expression, err)
		}
		if metadata.typ != c.typ || metadata.characterSet != c.charset {
			t.Errorf("scalarColumn(%q) type=%#x charset=%d, want type=%#x charset=%d", c.expression, metadata.typ, metadata.characterSet, c.typ, c.charset)
		}
		if unsigned := metadata.flags&mysqlUnsignedFlag != 0; unsigned != c.unsigned {
			t.Errorf("scalarColumn(%q) unsigned=%v, want %v", c.expression, unsigned, c.unsigned)
		}
	}
}

func TestScalarColumnDecimalReportsScale(t *testing.T) {
	_, _, metadata, err := scalarColumn("1.250")
	if err != nil {
		t.Fatalf("scalarColumn(1.250) error: %v", err)
	}
	if metadata.decimals != 3 {
		t.Errorf("scalarColumn(1.250) decimals = %d, want 3", metadata.decimals)
	}
}

func TestRoundIntegerPreservesMetadataDomain(t *testing.T) {
	_, _, metadata, err := scalarColumn("ROUND(15, -1)")
	if err != nil {
		t.Fatalf("scalarColumn(ROUND(15, -1)) error: %v", err)
	}
	if metadata.typ != mysqlTypeLongLong {
		t.Errorf("ROUND(15, -1) type = %#x, want integer type %#x", metadata.typ, mysqlTypeLongLong)
	}
	if metadata.flags&mysqlUnsignedFlag != 0 || metadata.decimals != 0 {
		t.Errorf("ROUND(15, -1) metadata = %#v, want signed integer with zero decimals", metadata)
	}
}

// TestParsedLiteralMatchesScalarColumn proves the prepared-metadata helper and
// the execution path derive identical columns, so text and prepared execution of
// the same expression agree.
func TestParsedLiteralMatchesScalarColumn(t *testing.T) {
	for _, expression := range []string{"7", "'Ada'", "1 + 1", "NULL", "1.5"} {
		value, isNull, metadata, err := scalarColumn(expression)
		if err != nil {
			t.Fatalf("scalarColumn(%q) error: %v", expression, err)
		}
		literal := parseLiteralResult(expression)
		if !literal.supported || literal.value != value || literal.isNull != isNull || literal.metadata != metadata {
			t.Errorf("parseLiteralResult(%q) diverged from scalarColumn", expression)
		}
	}
	if parseLiteralResult("1 / 0").supported {
		t.Errorf("parseLiteralResult reported an unsupported expression as supported")
	}
}

func TestPreparedScalarMetadataInfersParameterDomain(t *testing.T) {
	executor := expressionExecutor(t)
	preparation := &preparedPreparation{executor.session}
	metadata, err := preparation.preparedColumns("SELECT SUBSTRING(?, 2)")
	if err != nil || len(metadata) != 1 {
		t.Fatalf("parameterized SUBSTRING metadata unavailable: err=%v metadata=%#v", err, metadata)
	}
	if metadata[0].typ != mysqlTypeVarString || metadata[0].characterSet != mysqlCharsetUTF8MB40900AICI {
		t.Fatalf("parameterized SUBSTRING metadata = %#v, want utf8mb4 string", metadata[0])
	}
	if metadata[0].name != "SUBSTRING(?, 2)" {
		t.Fatalf("parameterized SUBSTRING metadata name = %q", metadata[0].name)
	}
}
