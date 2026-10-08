// This file owns the one portable rule for comparing SQL identifiers. A
// namespace, table, or column name preserves its declared spelling but is keyed
// for lookup and uniqueness by Unicode canonical caseless matching, so the same
// name collides on every supported platform regardless of case or canonically
// equivalent spelling, while compatibility variants stay distinct.
package catalog

import (
	"strings"
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

// SameIdentifier reports whether two SQL identifier spellings name the same
// object under canonical caseless matching. Call it instead of comparing raw
// spellings or using strings.EqualFold, which neither applies full case folding
// nor normalizes canonically equivalent forms.
func SameIdentifier(left, right string) bool {
	return Key(left) == Key(right)
}

// InformationSchemaName is the declared spelling of the read-only metadata
// namespace.
const InformationSchemaName = "information_schema"

// IsInformationSchema reports whether a namespace spelling names the read-only
// metadata namespace.
func IsInformationSchema(name string) bool {
	return SameIdentifier(name, InformationSchemaName)
}

// IdentifierLength counts the Unicode scalar values in a declared identifier
// spelling, the unit the length ceiling is measured in.
func IdentifierLength(name string) int {
	return utf8.RuneCountInString(name)
}

// TableRef is the canonical identity of one table: the identifier keys of its
// namespace and name. Every equivalent SQL spelling of a table yields an equal
// TableRef, so it is safe to use as a map key or to compare with ==. Its fields
// are unexported so a caller cannot build one from raw spellings.
type TableRef struct {
	namespace string
	table     string
}

// NewTableRef resolves a namespace and table spelling to its canonical identity.
func NewTableRef(namespace, table string) TableRef {
	return TableRef{namespace: Key(namespace), table: Key(table)}
}

// Compare orders table identities by namespace key and then by table key.
func (r TableRef) Compare(other TableRef) int {
	if order := strings.Compare(r.namespace, other.namespace); order != 0 {
		return order
	}
	return strings.Compare(r.table, other.table)
}
