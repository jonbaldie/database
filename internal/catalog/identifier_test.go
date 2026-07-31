package catalog

import "testing"

func TestKeyFoldsCaseAndCanonicalEquivalents(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		same  bool
	}{
		{"ascii case", "Users", "users", true},
		{"ascii mixed case", "MyTable", "mytable", true},
		{"accented case", "Café", "café", true},
		{"precomposed vs decomposed", "Å", "Å", true},
		{"full case fold sharp s", "STRASSE", "straße", true},
		{"ligature folds under full case fold", "ﬀ", "ff", true},
		{"distinct names", "orders", "order", false},
		{"accent is significant", "café", "cafe", false},
		{"fullwidth compatibility variant stays distinct", "Ａ", "A", false},
		{"circled compatibility variant stays distinct", "①", "1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Key(c.left) == Key(c.right)
			if got != c.same {
				t.Fatalf("Key(%q)==Key(%q) = %v, want %v (%q vs %q)", c.left, c.right, got, c.same, Key(c.left), Key(c.right))
			}
		})
	}
}

func TestKeyPreservesNothingButComparisonKey(t *testing.T) {
	// The stored spelling is the caller's; Key only returns a comparison key.
	if Key("Users") == "Users" {
		t.Fatalf("Key should fold case, got %q", Key("Users"))
	}
}

func TestIdentifierLengthCountsScalarValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  int
	}{
		{"ascii", "orders", 6},
		{"empty", "", 0},
		{"multibyte scalars", "café", 4},
		{"emoji scalar", "a\U0001F600b", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IdentifierLength(c.value); got != c.want {
				t.Fatalf("IdentifierLength(%q) = %d, want %d", c.value, got, c.want)
			}
		})
	}
}

func TestIdentifierLimitBoundary(t *testing.T) {
	sixtyFour := make([]rune, IdentifierLimit)
	for i := range sixtyFour {
		sixtyFour[i] = 'a'
	}
	if IdentifierLength(string(sixtyFour)) != IdentifierLimit {
		t.Fatalf("boundary length mismatch")
	}
	if IdentifierLength(string(append(sixtyFour, 'a'))) <= IdentifierLimit {
		t.Fatalf("over-limit identifier not detected")
	}
}
