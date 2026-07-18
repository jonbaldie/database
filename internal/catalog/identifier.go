// This file owns the one portable rule for comparing SQL identifiers. A
// namespace, table, or column name preserves its declared spelling but is keyed
// for lookup and uniqueness by Unicode canonical caseless matching, so the same
// name collides on every supported platform regardless of case or canonically
// equivalent spelling, while compatibility variants stay distinct.
package catalog

import (
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// IdentifierLimit is the fixed v0.1 ceiling on an identifier's declared length,
// counted in Unicode scalar values rather than bytes.
const IdentifierLimit = 64

// Key returns the canonical caseless matching key for a SQL identifier. Two
// names share a key exactly when Unicode canonical caseless matching treats them
// as equal: NFD(casefold(NFD(name))). Canonical decomposition keeps compatibility
// variants (for example a full-width or circled form) distinct, while full case
// folding makes case- and fold-equivalent names collide. The Unicode 17.0 tables
// come from golang.org/x/text, so the matching rule stays identical on every
// supported platform regardless of the host's own Unicode version.
func Key(name string) string {
	decomposed := norm.NFD.String(name)
	folded := cases.Fold().String(decomposed)
	return norm.NFD.String(folded)
}

// IdentifierLength counts the Unicode scalar values in a declared identifier
// spelling, the unit the length ceiling is measured in.
func IdentifierLength(name string) int {
	return utf8.RuneCountInString(name)
}
