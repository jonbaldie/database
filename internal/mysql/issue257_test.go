package mysql

import "testing"

// TestIssue257NonZeroPaddedTemporalComponents covers date and datetime literals
// whose month, day, hour, minute, and second fields carry fewer than two digits.
// MySQL accepts these spellings and normalizes them to the canonical padded
// form, so inserts must accept them, and stored values must come back
// zero-padded.
func TestIssue257NonZeroPaddedTemporalComponents(t *testing.T) {
	cases := []struct {
		name     string
		date     string
		datetime string
		want     string
	}{
		{
			name:     "single_digit_month",
			date:     "'2020-1-15'",
			datetime: "'2020-1-15 0:0:0'",
			want:     "2020-01-15 00:00:00",
		},
		{
			name:     "single_digit_day",
			date:     "'2020-01-5'",
			datetime: "'2020-01-5 9:3:7'",
			want:     "2020-01-05 09:03:07",
		},
		{
			name:     "single_digit_month_day_and_clock",
			date:     "'2020-1-5'",
			datetime: "'2020-1-5 9:3:7'",
			want:     "2020-01-05 09:03:07",
		},
		{
			name:     "fully_padded_control",
			date:     "'2020-01-05'",
			datetime: "'2020-01-05 09:03:07'",
			want:     "2020-01-05 09:03:07",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			for _, query := range []string{
				"CREATE TABLE t (id INT PRIMARY KEY, d DATE, ts DATETIME)",
				"INSERT INTO t VALUES (1, " + test.date + ", " + test.datetime + ")",
			} {
				if _, err := executeStatement(executor, query); err != nil {
					t.Fatalf("execute %q: %v", query, err)
				}
			}
			result, err := executeStatement(executor, "SELECT d, ts FROM t WHERE id = 1")
			if err != nil {
				t.Fatalf("select: %v", err)
			}
			if len(result.rows) != 1 || result.rows[0][0] != test.want[:10] || result.rows[0][1] != test.want {
				t.Fatalf("stored = %v, want date %s and datetime %s", result.rows, test.want[:10], test.want)
			}
		})
	}
}

// TestIssue257NonZeroPaddedComparisons checks that a comparison against a
// temporal column accepts non-padded input and matches the padded canonical
// value already stored.
func TestIssue257NonZeroPaddedComparisons(t *testing.T) {
	executor := ddlExecutorForTest(t)
	for _, query := range []string{
		"CREATE TABLE t (id INT PRIMARY KEY, d DATE, ts DATETIME)",
		"INSERT INTO t VALUES (1, '2020-01-15', '2020-01-05 09:03:07')",
	} {
		if _, err := executeStatement(executor, query); err != nil {
			t.Fatalf("execute %q: %v", query, err)
		}
	}
	matched, err := executeStatement(executor, "SELECT id FROM t WHERE d = '2020-1-15'")
	if err != nil {
		t.Fatalf("date comparison: %v", err)
	}
	if len(matched.rows) != 1 || matched.rows[0][0] != "1" {
		t.Fatalf("date comparison = %v, want [[1]]", matched.rows)
	}
	matched, err = executeStatement(executor, "SELECT id FROM t WHERE ts = '2020-1-5 9:3:7'")
	if err != nil {
		t.Fatalf("datetime comparison: %v", err)
	}
	if len(matched.rows) != 1 || matched.rows[0][0] != "1" {
		t.Fatalf("datetime comparison = %v, want [[1]]", matched.rows)
	}
}

// TestIssue257StillRejectsInvalidTemporalValues keeps the malformed and
// out-of-range rejections the relaxed component widths must not loosen.
func TestIssue257StillRejectsInvalidTemporalValues(t *testing.T) {
	cases := []struct {
		name  string
		date  string
		clock string
	}{
		{name: "month_out_of_range", date: "'2020-13-1'"},
		{name: "day_out_of_range", date: "'2020-1-32'"},
		{name: "february_thirty", date: "'2020-2-30'"},
		{name: "three_digit_month", date: "'2020-001-15'"},
		{name: "three_digit_day", date: "'2020-01-015'"},
		{name: "two_digit_year", date: "'20-1-15'"},
		{name: "three_digit_year", date: "'020-1-15'"},
		{name: "non_digit_components", date: "'2020-x-15'"},
		{name: "hour_out_of_range", clock: "'2020-1-5 24:0:0'"},
		{name: "minute_out_of_range", clock: "'2020-1-5 9:60:0'"},
		{name: "second_out_of_range", clock: "'2020-1-5 9:0:60'"},
		{name: "three_digit_hour", clock: "'2020-1-5 009:0:0'"},
		{name: "non_digit_minute", clock: "'2020-1-5 9:x:0'"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			executor := ddlExecutorForTest(t)
			if _, err := executeStatement(executor, "CREATE TABLE t (id INT PRIMARY KEY, d DATE, ts DATETIME)"); err != nil {
				t.Fatalf("create: %v", err)
			}
			literal := test.date
			if literal == "" {
				literal = test.clock
			}
			if _, err := executeStatement(executor, "INSERT INTO t VALUES (1, "+literal+", "+literal+")"); err == nil {
				t.Fatalf("accepted invalid temporal value %s", literal)
			}
		})
	}
}
