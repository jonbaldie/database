package mysql

import (
	"strings"
	"testing"
)

func TestCompileRelationPredicateHandlesLargeLogicalExpressions(t *testing.T) {
	columns := []relationColumn{{name: "id", typeName: "INT", index: 0}}
	session := &session{timeZone: "UTC"}
	tests := []struct {
		name string
		text string
		row  string
		want bool
	}{
		{
			name: "long flat conjunction",
			text: strings.Repeat("id >= 1 AND ", 99) + "id >= 1",
			row:  "2",
			want: true,
		},
		{
			name: "deep parentheses",
			text: strings.Repeat("(", 128) + "id = 1" + strings.Repeat(")", 128),
			row:  "1",
			want: true,
		},
		{
			name: "AND binds more tightly than OR",
			text: "id = 1 OR id = 2 AND id = 3",
			row:  "1",
			want: true,
		},
		{
			name: "parentheses override precedence",
			text: "(id = 1 OR id = 2) AND (id = 2 OR id = 3)",
			row:  "1",
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			predicate, err := compileRelationPredicate(test.text, columns, session)
			if err != nil {
				t.Fatal(err)
			}
			result, err := predicate(relationRow{values: []string{test.row}})
			if err != nil {
				t.Fatal(err)
			}
			known, truth, err := truthValue(result)
			if err != nil {
				t.Fatal(err)
			}
			if !known || truth != test.want {
				t.Fatalf("truth = (%t, %t), want (true, %t)", known, truth, test.want)
			}
		})
	}
}

func TestParseStoredNumericValueEquivalence(t *testing.T) {
	cases := []string{
		"0",
		"1",
		"-1",
		"42",
		"-999999",
		"9223372036854775807",
		"-9223372036854775808",
		"18446744073709551615",
		"12.34",
		"-45.678",
		"1e5",
		"-2.5e-3",
		"  100  ",
		"  -200  ",
	}
	for _, tc := range cases {
		got, err := parseStoredNumericValue(tc)
		want, wantErr := evaluateScalar(tc)
		if (err != nil) != (wantErr != nil) {
			t.Fatalf("parseStoredNumericValue(%q) err=%v, wantErr=%v", tc, err, wantErr)
		}
		if err == nil {
			if got.kind != want.kind || got.render() != want.render() {
				t.Fatalf("parseStoredNumericValue(%q) = (%v, %q), want (%v, %q)", tc, got.kind, got.render(), want.kind, want.render())
			}
		}
	}
}
