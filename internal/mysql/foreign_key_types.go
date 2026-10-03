package mysql

import (
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

func foreignKeyColumnTypesCompatible(child, parent catalog.Table, childIndex, parentIndex int) bool {
	childType, childKnown := child.ColumnType(childIndex)
	parentType, parentKnown := parent.ColumnType(parentIndex)
	if !childKnown || !parentKnown {
		// Older catalog entries can lack type metadata. Keep their existing
		// constraints usable because the original declarations are not known.
		return true
	}
	return compatibleForeignKeyTypeNames(childType, parentType)
}

func compatibleForeignKeyTypeNames(childType, parentType string) bool {
	if compatible, known := compatibleForeignKeyNumericTypeNames(childType, parentType); known {
		return compatible
	}
	if compatible, known := compatibleForeignKeyTemporalTypeNames(childType, parentType); known {
		return compatible
	}
	if compatible, known := compatibleForeignKeyCharacterTypeNames(childType, parentType); known {
		return compatible
	}
	return strings.EqualFold(strings.TrimSpace(childType), strings.TrimSpace(parentType))
}

func compatibleForeignKeyNumericTypeNames(childType, parentType string) (bool, bool) {
	child, childErr := parseNumericType(childType)
	parent, parentErr := parseNumericType(parentType)
	if childErr != nil || parentErr != nil {
		return false, true
	}
	if child.kind == numericNone && parent.kind == numericNone {
		return false, false
	}
	return compatibleForeignKeyNumericTypes(child, parent), true
}

func compatibleForeignKeyNumericTypes(child, parent numericType) bool {
	if foreignKeyIntegerType(child.kind) || foreignKeyIntegerType(parent.kind) {
		return compatibleForeignKeyIntegerTypes(child, parent)
	}
	if child.kind != parent.kind {
		return false
	}
	switch child.kind {
	case numericDecimal:
		return compatibleForeignKeyDecimals(child, parent)
	case numericFloat:
		return compatibleForeignKeyFloats(child, parent)
	case numericBit:
		return child.width == parent.width
	default:
		return false
	}
}

func foreignKeyIntegerType(kind numericKind) bool {
	return kind == numericInteger || kind == numericBoolean
}

func compatibleForeignKeyIntegerTypes(child, parent numericType) bool {
	return foreignKeyIntegerType(child.kind) && foreignKeyIntegerType(parent.kind) && child.wire == parent.wire && child.unsigned == parent.unsigned
}

func compatibleForeignKeyDecimals(child, parent numericType) bool {
	return child.precision == parent.precision && child.scale == parent.scale && child.unsigned == parent.unsigned
}

func compatibleForeignKeyFloats(child, parent numericType) bool {
	return child.sixtyFour == parent.sixtyFour && child.unsigned == parent.unsigned
}

func compatibleForeignKeyTemporalTypeNames(childType, parentType string) (bool, bool) {
	child, childErr := parseTemporalType(childType)
	parent, parentErr := parseTemporalType(parentType)
	if childErr != nil || parentErr != nil {
		return false, true
	}
	if child.kind == temporalNone && parent.kind == temporalNone {
		return false, false
	}
	return child.kind != temporalNone && child.kind == parent.kind && child.precision == parent.precision, true
}

func compatibleForeignKeyCharacterTypeNames(childType, parentType string) (bool, bool) {
	child, childErr := parseCharacterType(childType)
	parent, parentErr := parseCharacterType(parentType)
	if childErr != nil || parentErr != nil {
		return false, true
	}
	if child.kind == characterNone && parent.kind == characterNone {
		return false, false
	}
	if child.kind == characterNone || child.kind != parent.kind {
		return false, true
	}
	if child.kind == characterText {
		return child.collation == parent.collation, true
	}
	return true, true
}
