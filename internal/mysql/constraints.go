package mysql

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jonbaldie/database/internal/catalog"
)

func isTableConstraintDefinition(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "constraint ") || strings.HasPrefix(value, "primary key") || strings.HasPrefix(value, "unique ") || strings.HasPrefix(value, "foreign key") || strings.HasPrefix(value, "check ")
}

func splitColumnTypeAndModifiers(value string) (string, string) {
	for index := range value {
		if !wordBoundary(value, index) {
			continue
		}
		for _, keyword := range []string{"not", "null", "default", "primary", "unique", "references", "check"} {
			if strings.EqualFold(value[index:index+minConstraintLength(len(value)-index, len(keyword))], keyword) && wordEnd(value, index+len(keyword)) {
				return strings.TrimSpace(value[:index]), strings.TrimSpace(value[index:])
			}
		}
	}
	return strings.TrimSpace(value), ""
}

func wordBoundary(value string, index int) bool {
	return index == 0 || (!isSQLWord(rune(value[index-1])) && value[index-1] != '`')
}

func wordEnd(value string, index int) bool {
	return index >= len(value) || (!isSQLWord(rune(value[index])) && value[index] != '`')
}

func isSQLWord(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || value == '_'
}

func minConstraintLength(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func parseColumnModifiers(column, value string) (catalog.ColumnAttribute, []catalog.Constraint, error) {
	attribute := catalog.ColumnAttribute{Nullable: true}
	constraints := []catalog.Constraint{}
	for strings.TrimSpace(value) != "" {
		next, update, constraint, matched, err := parseColumnModifier(column, value, attribute)
		if err != nil {
			return catalog.ColumnAttribute{}, nil, err
		}
		if !matched {
			return catalog.ColumnAttribute{}, nil, sqlFailure{1235, "42000", "unsupported column modifier"}
		}
		value = next
		if update != nil {
			attribute = *update
		}
		if constraint.Type != "" {
			constraints = append(constraints, constraint)
		}
	}
	return attribute, constraints, nil
}

func parseColumnModifier(column, value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	value = strings.TrimSpace(value)
	for _, parser := range []func(string, catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error){notNullModifier, nullModifier, defaultModifier, primaryModifier, uniqueModifier, checkModifier} {
		next, update, constraint, matched, err := parser(value, attribute)
		if matched || err != nil {
			if constraint.Type == "primary" || constraint.Type == "unique" {
				constraint.Columns = []string{column}
			}
			return next, update, constraint, matched, err
		}
	}
	return "", nil, catalog.Constraint{}, false, nil
}

func notNullModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "not null") || !wordEnd(value, len("not null")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	attribute.Nullable = false
	return value[len("NOT NULL"):], &attribute, catalog.Constraint{}, true, nil
}
func nullModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "null") || !wordEnd(value, len("null")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	attribute.Nullable = true
	return value[len("NULL"):], &attribute, catalog.Constraint{}, true, nil
}
func defaultModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "default ") {
		return "", nil, catalog.Constraint{}, false, nil
	}
	defaultValue, remainder, ok := consumeConstraintValue(value[len("DEFAULT "):])
	if !ok {
		return "", nil, catalog.Constraint{}, true, sqlFailure{1064, "42000", "invalid DEFAULT value"}
	}
	if !validDefaultLiteral(defaultValue) {
		return "", nil, catalog.Constraint{}, true, sqlFailure{1064, "42000", "DEFAULT requires a literal value"}
	}
	attribute.HasDefault, attribute.Default = true, defaultValue
	return remainder, &attribute, catalog.Constraint{}, true, nil
}

func validDefaultLiteral(value string) bool {
	if isSQLNullLiteral(value) {
		return true
	}
	_, err := evaluateScalar(value)
	return err == nil
}
func primaryModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "primary key") || !wordEnd(value, len("primary key")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	attribute.Nullable = false
	return value[len("PRIMARY KEY"):], &attribute, catalog.Constraint{Type: "primary"}, true, nil
}
func uniqueModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "unique") || !wordEnd(value, len("unique")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	return value[len("UNIQUE"):], nil, catalog.Constraint{Type: "unique"}, true, nil
}
func checkModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "check") || !wordEnd(value, len("check")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	expression, remainder, ok := consumeParenthesized(value[len("CHECK"):])
	if !ok || strings.TrimSpace(expression) == "" {
		return "", nil, catalog.Constraint{}, true, sqlFailure{1064, "42000", "invalid CHECK constraint"}
	}
	return remainder, nil, catalog.Constraint{Type: "check", Check: expression}, true, nil
}

func consumeConstraintValue(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if value[0] != '\'' {
		for index, character := range value {
			if unicode.IsSpace(character) {
				return value[:index], value[index:], index > 0
			}
		}
		return value, "", true
	}
	limit := len(value)
	for index := 1; index < limit; index++ {
		if value[index] != '\'' {
			continue
		}
		if index+1 < len(value) && value[index+1] == '\'' {
			index++
			continue
		}
		return value[:index+1], value[index+1:], true
	}
	return "", "", false
}

func parseTableConstraint(value string) (catalog.Constraint, error) {
	value = strings.TrimSpace(value)
	constraint := catalog.Constraint{}
	if strings.HasPrefix(strings.ToLower(value), "constraint ") {
		name, remainder, ok := consumeIdentifier(strings.TrimSpace(value[len("CONSTRAINT "):]))
		if !ok || validateIdentifierLength(name) != nil {
			return catalog.Constraint{}, sqlFailure{1064, "42000", "invalid constraint name"}
		}
		constraint.Name, value = name, strings.TrimSpace(remainder)
	}
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "primary key"):
		return parsePrimaryConstraint(constraint, value)
	case strings.HasPrefix(lower, "unique"):
		return parseUniqueConstraint(constraint, value)
	case strings.HasPrefix(lower, "foreign key"):
		return parseForeignConstraint(constraint, value)
	case strings.HasPrefix(lower, "check"):
		return parseCheckConstraint(constraint, value)
	default:
		return catalog.Constraint{}, sqlFailure{1235, "42000", "unsupported table constraint"}
	}
}

func parsePrimaryConstraint(constraint catalog.Constraint, value string) (catalog.Constraint, error) {
	columns, remainder, ok := parseConstraintColumns(value[len("PRIMARY KEY"):])
	if !ok || strings.TrimSpace(remainder) != "" {
		return catalog.Constraint{}, sqlFailure{1064, "42000", "invalid PRIMARY KEY constraint"}
	}
	constraint.Type, constraint.Columns = "primary", columns
	return constraint, nil
}
func parseUniqueConstraint(constraint catalog.Constraint, value string) (catalog.Constraint, error) {
	value = stripOptionalKeyword(strings.TrimSpace(value[len("UNIQUE"):]), "key")
	if !strings.HasPrefix(strings.TrimSpace(value), "(") {
		name, remainder, ok := consumeIdentifier(value)
		if !ok {
			return catalog.Constraint{}, sqlFailure{1064, "42000", "invalid UNIQUE constraint"}
		}
		if constraint.Name == "" {
			constraint.Name = name
		}
		value = remainder
	}
	columns, remainder, ok := parseConstraintColumns(value)
	if !ok || strings.TrimSpace(remainder) != "" {
		return catalog.Constraint{}, sqlFailure{1064, "42000", "invalid UNIQUE constraint"}
	}
	constraint.Type, constraint.Columns = "unique", columns
	return constraint, nil
}
func parseForeignConstraint(constraint catalog.Constraint, value string) (catalog.Constraint, error) {
	columns, remainder, ok := parseConstraintColumns(value[len("FOREIGN KEY"):])
	if !ok {
		return catalog.Constraint{}, sqlFailure{1064, "42000", "invalid FOREIGN KEY constraint"}
	}
	target, referenced, err := parseForeignReference(remainder)
	if err != nil {
		return catalog.Constraint{}, err
	}
	constraint.Type, constraint.Columns, constraint.ReferencedColumns = "foreign_key", columns, referenced
	if len(target) == 2 {
		constraint.ReferencedNamespace, constraint.ReferencedTable = target[0], target[1]
	} else {
		constraint.ReferencedTable = target[0]
	}
	return constraint, nil
}
func parseForeignReference(value string) ([]string, []string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "references ") {
		return nil, nil, sqlFailure{1064, "42000", "FOREIGN KEY requires REFERENCES"}
	}
	target, remainder, ok := leadingIdentifierToken(strings.TrimSpace(value[len("REFERENCES "):]))
	parts, valid := splitQualifiedIdentifier(target)
	if !ok || !valid || len(parts) == 0 || len(parts) > 2 {
		return nil, nil, sqlFailure{1064, "42000", "invalid referenced table"}
	}
	referenced, remainder, ok := parseConstraintColumns(remainder)
	if !ok || strings.TrimSpace(remainder) != "" {
		return nil, nil, sqlFailure{1064, "42000", "invalid referenced columns"}
	}
	return parts, referenced, nil
}
func parseCheckConstraint(constraint catalog.Constraint, value string) (catalog.Constraint, error) {
	expression, remainder, ok := consumeParenthesized(value[len("CHECK"):])
	if !ok || strings.TrimSpace(expression) == "" || strings.TrimSpace(remainder) != "" {
		return catalog.Constraint{}, sqlFailure{1064, "42000", "invalid CHECK constraint"}
	}
	constraint.Type, constraint.Check = "check", expression
	return constraint, nil
}

func parseConstraintColumns(value string) ([]string, string, bool) {
	body, remainder, ok := consumeParenthesized(value)
	if !ok {
		return nil, "", false
	}
	parts := splitCSV(body)
	columns := make([]string, len(parts))
	for index, part := range parts {
		name, valid := singleIdentifier(strings.TrimSpace(part))
		if !valid {
			return nil, "", false
		}
		columns[index] = name
	}
	return columns, remainder, len(columns) > 0
}

func consumeParenthesized(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "(") {
		return "", "", false
	}
	depth := 0
	quoted := false
	for index, character := range value {
		if character == '\'' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(value[1:index]), value[index+1:], true
			}
		}
	}
	return "", "", false
}

func namedTableConstraints(table string, constraints []catalog.Constraint) ([]catalog.Constraint, error) {
	seen := map[string]bool{}
	checkNumber, foreignNumber := 0, 0
	for index := range constraints {
		constraint := &constraints[index]
		if len(constraint.Columns) == 0 && constraint.Type != "check" {
			return nil, sqlFailure{1064, "42000", "constraint requires columns"}
		}
		checkNumber, foreignNumber = assignConstraintName(table, constraint, checkNumber, foreignNumber)
		key := catalog.Key(constraint.Name)
		if seen[key] {
			return nil, sqlFailure{1061, "42000", "duplicate constraint name '" + constraint.Name + "'"}
		}
		seen[key] = true
	}
	primary := 0
	for _, constraint := range constraints {
		if constraint.Type == "primary" {
			primary++
		}
	}
	if primary > 1 {
		return nil, sqlFailure{1068, "42000", "multiple primary keys"}
	}
	return constraints, nil
}

func assignConstraintName(table string, constraint *catalog.Constraint, checkNumber, foreignNumber int) (int, int) {
	if constraint.Type == "primary" {
		constraint.Name = "PRIMARY"
	}
	if constraint.Type == "unique" && constraint.Name == "" {
		constraint.Name = table + "_" + constraint.Columns[0] + "_unique"
	}
	if constraint.Type == "check" && constraint.Name == "" {
		checkNumber++
		constraint.Name = fmt.Sprintf("%s_chk_%d", table, checkNumber)
	}
	if constraint.Type == "foreign_key" && constraint.Name == "" {
		foreignNumber++
		constraint.Name = fmt.Sprintf("%s_ibfk_%d", table, foreignNumber)
	}
	return checkNumber, foreignNumber
}

func cloneCatalogConstraints(source []catalog.Constraint) []catalog.Constraint {
	copy := make([]catalog.Constraint, len(source))
	for index, constraint := range source {
		copy[index] = constraint
		copy[index].Columns = append([]string(nil), constraint.Columns...)
		copy[index].ReferencedColumns = append([]string(nil), constraint.ReferencedColumns...)
	}
	return copy
}
