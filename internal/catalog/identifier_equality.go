package catalog

// SameIdentifier reports whether two SQL identifier spellings name the same
// object under the canonical caseless matching rule that Key defines. Call it
// instead of comparing raw spellings or using strings.EqualFold, which neither
// applies full case folding nor normalizes canonically equivalent forms.
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
