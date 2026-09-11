package mysql

import "testing"

func TestDecodeMySQLStringValue(t *testing.T) {
	for input, want := range map[string]string{
		`a\\b`:    `a\b`,
		`it\'s`:   "it's",
		`a\"b`:    `a"b`,
		`line1\n`: "line1\n",
		`a\0b`:    "a\x00b",
		`a\rb`:    "a\rb",
		`a\tb`:    "a\tb",
		`a\bb`:    "a\bb",
		`a\Zb`:    "a\x1ab",
		`a\%b`:    `a\%b`,
		`a\_b`:    `a\_b`,
		`a\xb`:    "axb",
		`it''s`:   "it's",
	} {
		got, ok := decodeMySQLStringValue(input, '\'')
		if !ok || got != want {
			t.Errorf("decodeMySQLStringValue(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
}

func TestDecodeMySQLDoubleQuotedStringValue(t *testing.T) {
	for input, want := range map[string]string{
		`it""s`: `it"s`,
		`it\"s`: `it"s`,
		`it's`:  "it's",
		`it''s`: "it''s",
	} {
		got, ok := decodeMySQLStringValue(input, '"')
		if !ok || got != want {
			t.Errorf("decodeMySQLStringValue(%q, '\"') = %q, %v; want %q, true", input, got, ok, want)
		}
	}
}

func TestQuoteRoundTripsMySQLStringValue(t *testing.T) {
	for _, want := range []string{`a\b`, `a\xb`, `a\%b`, "it's", "line1\nline2"} {
		quoted := quote(want)
		token, next, err := scanString(quoted, 0)
		if err != nil {
			t.Fatalf("scanString(%q): %v", quoted, err)
		}
		if next != len(quoted) || token.str != want {
			t.Errorf("quote(%q) = %q; decoded %q at %d, want %q at %d", want, quoted, token.str, next, want, len(quoted))
		}
	}
}
