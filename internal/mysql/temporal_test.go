package mysql

import (
	"testing"
	"time"
)

func TestParseTemporalTypeRecognisesFamilies(t *testing.T) {
	cases := map[string]temporalKind{
		"DATE":         temporalDate,
		"TIME":         temporalTime,
		"TIME(3)":      temporalTime,
		"DATETIME":     temporalDatetime,
		"DATETIME(6)":  temporalDatetime,
		"TIMESTAMP":    temporalTimestamp,
		"TIMESTAMP(0)": temporalTimestamp,
		"YEAR":         temporalYear,
		"INT":          temporalNone,
		"VARCHAR(8)":   temporalNone,
		"":             temporalNone,
	}
	for input, want := range cases {
		got, err := parseTemporalType(input)
		if err != nil {
			t.Fatalf("parseTemporalType(%q) unexpected error: %v", input, err)
		}
		if got.kind != want {
			t.Errorf("parseTemporalType(%q).kind = %v, want %v", input, got.kind, want)
		}
	}
}

func TestParseTemporalTypeReadsPrecision(t *testing.T) {
	typ, err := parseTemporalType("DATETIME(4)")
	if err != nil || typ.precision != 4 {
		t.Fatalf("parseTemporalType(DATETIME(4)) precision = %d err %v", typ.precision, err)
	}
	if typ, _ := parseTemporalType("TIMESTAMP"); typ.precision != 0 {
		t.Errorf("bare TIMESTAMP precision = %d, want 0", typ.precision)
	}
}

func TestParseTemporalTypeRejectsInvalidDeclarations(t *testing.T) {
	for _, input := range []string{
		"DATETIME(7)", "TIME(-1)", "TIMESTAMP(9)", "DATETIME(x)",
		"DATE(3)", "YEAR(4)", "YEAR(2)",
	} {
		if _, err := parseTemporalType(input); err == nil {
			t.Errorf("parseTemporalType(%q) accepted an invalid declaration", input)
		}
	}
}

func TestCanonicalTemporalValueAccepts(t *testing.T) {
	cases := []struct {
		typeName, in, want string
	}{
		{"DATE", "2021-01-02", "2021-01-02"},
		{"DATE", "1000-01-01", "1000-01-01"},
		{"DATE", "9999-12-31", "9999-12-31"},
		{"DATE", "2020-02-29", "2020-02-29"},
		{"YEAR", "1901", "1901"},
		{"YEAR", "2155", "2155"},
		{"TIME", "12:34:56", "12:34:56"},
		{"TIME", "-838:59:59", "-838:59:59"},
		{"TIME", "838:59:59", "838:59:59"},
		{"TIME(3)", "12:34:56.789", "12:34:56.789"},
		{"TIME(3)", "12:34:56.7", "12:34:56.700"},
		{"TIME(3)", "12:34:56", "12:34:56.000"},
		{"DATETIME", "2021-01-02 03:04:05", "2021-01-02 03:04:05"},
		{"DATETIME(6)", "2021-01-02 03:04:05.123456", "2021-01-02 03:04:05.123456"},
		{"TIMESTAMP", "1970-01-01 00:00:01", "1970-01-01 00:00:01"},
		{"TIMESTAMP", "2038-01-19 03:14:07", "2038-01-19 03:14:07"},
		{"DATE", "null", "NULL"},
	}
	for _, c := range cases {
		typ, _ := parseTemporalType(c.typeName)
		got, err := canonicalTemporalValue(typ, c.in, "col", 1)
		if err != nil {
			t.Fatalf("canonicalTemporalValue(%s,%q) error: %v", c.typeName, c.in, err)
		}
		if got != c.want {
			t.Errorf("canonicalTemporalValue(%s,%q) = %q, want %q", c.typeName, c.in, got, c.want)
		}
	}
}

func TestCanonicalTemporalValueRejects(t *testing.T) {
	cases := []struct{ typeName, in string }{
		{"DATE", "0000-00-00"},               // zero date
		{"DATE", "2021-00-05"},               // zero month
		{"DATE", "2021-01-00"},               // zero day
		{"DATE", "2021-02-29"},               // invalid calendar (not a leap year)
		{"DATE", "2021-13-01"},               // month out of range
		{"DATE", "999-01-01"},                // out of range year
		{"DATE", "21-01-01"},                 // two-digit year ambiguity
		{"DATE", "2021/01/01"},               // ambiguous separator
		{"DATE", "20210101"},                 // ambiguous packed form
		{"DATE", "2021-01-02 00:00:00"},      // datetime into DATE
		{"YEAR", "0000"},                     // zero year
		{"YEAR", "1900"},                     // below range
		{"YEAR", "2156"},                     // above range
		{"YEAR", "69"},                       // two-digit expansion
		{"TIME", "839:00:00"},                // out of range
		{"TIME", "12:60:00"},                 // minute out of range
		{"TIME", "12:00:60"},                 // second out of range
		{"TIME(0)", "12:00:00.5"},            // excess fractional precision
		{"TIME(3)", "12:00:00.7890"},         // excess fractional precision
		{"DATETIME", "0000-00-00 00:00:00"},  // zero datetime
		{"DATETIME", "2021-02-30 00:00:00"},  // invalid calendar
		{"DATETIME", "2021-01-02"},           // date into DATETIME
		{"TIMESTAMP", "1970-01-01 00:00:00"}, // below TIMESTAMP epoch floor
		{"TIMESTAMP", "2038-01-19 03:14:08"}, // above TIMESTAMP ceiling
		{"TIMESTAMP", "0000-00-00 00:00:00"}, // zero timestamp
	}
	for _, c := range cases {
		typ, _ := parseTemporalType(c.typeName)
		if _, err := canonicalTemporalValue(typ, c.in, "col", 1); err == nil {
			t.Errorf("canonicalTemporalValue(%s,%q) accepted an invalid value", c.typeName, c.in)
		}
	}
}

func TestTemporalWireType(t *testing.T) {
	cases := []struct {
		typeName string
		wire     byte
		length   uint32
	}{
		{"DATE", mysqlTypeDate, 10},
		{"YEAR", mysqlTypeYear, 4},
		{"TIME", mysqlTypeTime, 10},
		{"TIME(3)", mysqlTypeTime, 14},
		{"DATETIME", mysqlTypeDatetime, 19},
		{"DATETIME(6)", mysqlTypeDatetime, 26},
		{"TIMESTAMP", mysqlTypeTimestamp, 19},
	}
	for _, c := range cases {
		typ, _ := parseTemporalType(c.typeName)
		wire, length, charset := temporalWireType(typ)
		if wire != c.wire || length != c.length || charset != mysqlCharsetBinary {
			t.Errorf("temporalWireType(%s) = (%#x,%d,%d), want (%#x,%d,%d)", c.typeName, wire, length, charset, c.wire, c.length, mysqlCharsetBinary)
		}
	}
}

func TestParseFixedOffset(t *testing.T) {
	cases := map[string]int{
		"UTC":    0,
		"+00:00": 0,
		"+05:30": 330,
		"-08:00": -480,
		"+14:00": 840,
	}
	for input, want := range cases {
		got, err := parseFixedOffset(input)
		if err != nil {
			t.Fatalf("parseFixedOffset(%q) error: %v", input, err)
		}
		if got != want {
			t.Errorf("parseFixedOffset(%q) = %d, want %d", input, got, want)
		}
	}
	for _, bad := range []string{"", "Europe/London", "+5:30", "+15:00", "-15:00", "+05:60", "SYSTEM"} {
		if _, err := parseFixedOffset(bad); err == nil {
			t.Errorf("parseFixedOffset(%q) accepted an unsupported zone", bad)
		}
	}
}

func TestRenderTimestampFixedOffset(t *testing.T) {
	// An instant is UTC; rendering applies the fixed session offset.
	got, err := renderTimestampFixedOffset("1970-01-01 00:00:01", 330, 0)
	if err != nil {
		t.Fatalf("renderTimestampFixedOffset error: %v", err)
	}
	if got != "1970-01-01 05:30:01" {
		t.Errorf("render +05:30 = %q, want 1970-01-01 05:30:01", got)
	}
	if got, _ := renderTimestampFixedOffset("2000-06-15 12:00:00", -480, 3); got != "2000-06-15 04:00:00.000" {
		t.Errorf("render -08:00 precision 3 = %q, want 2000-06-15 04:00:00.000", got)
	}
	if got, err := renderTimestampFixedOffset("2000-06-15 12:00:00.123456", -480, 3); err != nil || got != "2000-06-15 04:00:00.123" {
		t.Errorf("render -08:00 fractional precision 3 = %q err %v, want 2000-06-15 04:00:00.123", got, err)
	}
	// Two renderings of the same instant under the same offset are identical.
	first, _ := renderTimestampFixedOffset("2020-03-01 09:15:30", 0, 0)
	second, _ := renderTimestampFixedOffset("2020-03-01 09:15:30", 0, 0)
	if first != second {
		t.Errorf("fixed-offset rendering not reproducible: %q vs %q", first, second)
	}
}

func TestCurrentTemporalIsStableForAnInstant(t *testing.T) {
	instant := time.Date(2026, 7, 18, 14, 5, 6, 123456789, time.UTC)
	if got := currentTemporal(instant, temporalDate, 0); got != "2026-07-18" {
		t.Errorf("currentTemporal DATE = %q, want 2026-07-18", got)
	}
	if got := currentTemporal(instant, temporalTime, 0); got != "14:05:06" {
		t.Errorf("currentTemporal TIME = %q, want 14:05:06", got)
	}
	if got := currentTemporal(instant, temporalDatetime, 3); got != "2026-07-18 14:05:06.123" {
		t.Errorf("currentTemporal DATETIME(3) = %q, want 2026-07-18 14:05:06.123", got)
	}
	// The same instant always renders the same current-time value within a statement.
	if a, b := currentTemporal(instant, temporalDatetime, 6), currentTemporal(instant, temporalDatetime, 6); a != b {
		t.Errorf("current-time not stable for a fixed instant: %q vs %q", a, b)
	}
}
