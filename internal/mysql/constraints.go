package mysql

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jonbaldie/database/internal/catalog"
)

func isTableConstraintDefinition(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "constraint ") ||
		(strings.HasPrefix(value, "primary key") && wordEnd(value, len("primary key"))) ||
		(strings.HasPrefix(value, "unique") && wordEnd(value, len("unique"))) ||
		(strings.HasPrefix(value, "foreign key") && wordEnd(value, len("foreign key"))) ||
		(strings.HasPrefix(value, "check") && wordEnd(value, len("check")))
}

func splitColumnTypeAndModifiers(value string) (string, string) {
	for index := range value {
		if !wordBoundary(value, index) {
			continue
		}
		for _, keyword := range []string{"not", "null", "default", "auto_increment", "primary", "unique", "references", "check"} {
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

func parseColumnModifiers(column, value string) (catalog.ColumnAttribute, bool, []catalog.Constraint, error) {
	attribute := catalog.ColumnAttribute{Nullable: true}
	constraints := []catalog.Constraint{}
	statesNullability := false
	for strings.TrimSpace(value) != "" {
		next, update, constraint, matched, modifierStatesNullability, err := parseColumnModifier(column, value, attribute)
		statesNullability = statesNullability || modifierStatesNullability
		if err != nil {
			return catalog.ColumnAttribute{}, false, nil, err
		}
		if !matched {
			return catalog.ColumnAttribute{}, false, nil, sqlFailure{1235, "42000", "unsupported column modifier"}
		}
		value = next
		if update != nil {
			attribute = *update
		}
		if constraint.Type != "" {
			constraints = append(constraints, constraint)
		}
	}
	return attribute, statesNullability, constraints, nil
}

type columnModifierParser struct {
	statesNullability bool
	parse             func(string, catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error)
}

func parseColumnModifier(column, value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, bool, error) {
	value = strings.TrimSpace(value)
	for _, parser := range columnModifierParsers {
		next, update, constraint, matched, err := parser.parse(value, attribute)
		if matched || err != nil {
			if constraint.Type == catalog.ConstraintTypePrimary || constraint.Type == catalog.ConstraintTypeUnique {
				constraint.Columns = []string{column}
			}
			if err == nil && constraint.Type == catalog.ConstraintTypeCheck && columnCheckReferencesOtherColumn(column, constraint.Check) {
				return "", nil, catalog.Constraint{}, true, false, errorsConstraintDefinition("Column check constraint references other column.")
			}
			return next, update, constraint, matched, parser.statesNullability, err
		}
	}
	return "", nil, catalog.Constraint{}, false, false, nil
}

var columnModifierParsers = []columnModifierParser{
	{statesNullability: true, parse: notNullModifier},
	{statesNullability: true, parse: nullModifier},
	{parse: defaultModifier},
	{parse: autoIncrementModifier},
	{parse: primaryModifier},
	{parse: uniqueModifier},
	{parse: checkModifier},
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

func autoIncrementModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	const keyword = "AUTO_INCREMENT"
	if !strings.HasPrefix(strings.ToLower(value), strings.ToLower(keyword)) || !wordEnd(value, len(keyword)) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	if attribute.AutoIncrement {
		return "", nil, catalog.Constraint{}, true, sqlFailure{1075, "42000", "Incorrect table definition; there can be only one auto column and it must be defined as a key"}
	}
	attribute.AutoIncrement = true
	return value[len(keyword):], &attribute, catalog.Constraint{}, true, nil
}

func validDefaultLiteral(value string) bool {
	tokens, err := tokenizeExpression(value)
	if err != nil {
		return false
	}
	return validDefaultLiteralTokens(tokens)
}

func validDefaultLiteralTokens(tokens []exprToken) bool {
	if len(tokens) == 1 {
		return validBareDefaultLiteral(tokens[0])
	}
	return len(tokens) == 2 && (tokens[0].text == "+" || tokens[0].text == "-") && tokens[1].kind == tokenNumber
}

func validBareDefaultLiteral(token exprToken) bool {
	switch token.kind {
	case tokenNumber, tokenString:
		return true
	case tokenIdent:
		return isSQLNullLiteral(token.text) || strings.EqualFold(token.text, "true") || strings.EqualFold(token.text, "false")
	default:
		return false
	}
}
func primaryModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "primary key") || !wordEnd(value, len("primary key")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	attribute.Nullable = false
	return value[len("PRIMARY KEY"):], &attribute, catalog.Constraint{Type: catalog.ConstraintTypePrimary}, true, nil
}
func uniqueModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "unique") || !wordEnd(value, len("unique")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	return value[len("UNIQUE"):], nil, catalog.Constraint{Type: catalog.ConstraintTypeUnique}, true, nil
}
func checkModifier(value string, attribute catalog.ColumnAttribute) (string, *catalog.ColumnAttribute, catalog.Constraint, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), "check") || !wordEnd(value, len("check")) {
		return "", nil, catalog.Constraint{}, false, nil
	}
	expression, remainder, ok := consumeParenthesized(value[len("CHECK"):])
	if !ok || strings.TrimSpace(expression) == "" {
		return "", nil, catalog.Constraint{}, true, sqlFailure{1064, "42000", "invalid CHECK constraint"}
	}
	return remainder, nil, catalog.Constraint{Type: catalog.ConstraintTypeCheck, Check: expression}, true, nil
}

// columnCheckReferencesOtherColumn reports whether a column-level CHECK names
// any column other than the one it is declared on. MySQL accepts only the owning
// column there; a check across columns must be a table-level constraint.
func columnCheckReferencesOtherColumn(column, expression string) bool {
	other := false
	_, _ = evaluateScalarResolved(expression, func(name string) (exprValue, error) {
		parts, valid := splitQualifiedIdentifier(strings.TrimSpace(name))
		if !valid || len(parts) == 0 || !identifiersEqual(parts[len(parts)-1], column) {
			other = true
		}
		return nullValue(), nil
	}, nil)
	return other
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
	if end, ok := quotedSQLLiteralEnd(value, 0); ok {
		return value[:end+1], value[end+1:], true
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
	constraint.Type, constraint.Columns = catalog.ConstraintTypePrimary, columns
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
	constraint.Type, constraint.Columns = catalog.ConstraintTypeUnique, columns
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
	constraint.Type, constraint.Columns, constraint.ReferencedColumns = catalog.ConstraintTypeForeignKey, columns, referenced
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
	constraint.Type, constraint.Check = catalog.ConstraintTypeCheck, expression
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
	valueLength := len(value)
	for index := 0; index < valueLength; index++ {
		character := value[index]
		if isSQLQuote(character) {
			end, ok := quotedSQLLiteralEnd(value, index)
			if !ok {
				return "", "", false
			}
			index = end
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

type tableKeyRef struct {
	isIndex bool
	offset  int
}

func namedTableConstraints(table string, constraints []catalog.Constraint, reservedKeys map[string]bool) ([]catalog.Constraint, error) {
	if err := prepareConstraintSymbols(table, constraints); err != nil {
		return nil, err
	}
	taken := copyKeyNames(reservedKeys)
	for index := range constraints {
		constraint := &constraints[index]
		if !isIndexedConstraint(*constraint) {
			continue
		}
		if err := assignIndexedConstraintName(constraint, taken); err != nil {
			return nil, err
		}
	}
	if err := rejectMultiplePrimaryKeys(constraints); err != nil {
		return nil, err
	}
	return constraints, nil
}

func nameCreateTableKeys(table string, constraints []catalog.Constraint, indexes []catalog.Index, order []tableKeyRef) ([]catalog.Constraint, []catalog.Index, error) {
	if err := prepareConstraintSymbols(table, constraints); err != nil {
		return nil, nil, err
	}
	namedIndexes := catalog.CloneIndexes(indexes)
	taken := map[string]bool{}
	for _, ref := range order {
		if err := assignCreateTableKey(table, constraints, namedIndexes, ref, taken); err != nil {
			return nil, nil, err
		}
	}
	if err := rejectMultiplePrimaryKeys(constraints); err != nil {
		return nil, nil, err
	}
	return constraints, namedIndexes, nil
}

func assignCreateTableKey(table string, constraints []catalog.Constraint, indexes []catalog.Index, ref tableKeyRef, taken map[string]bool) error {
	if ref.isIndex {
		return assignIndexName(table, &indexes[ref.offset], taken)
	}
	return assignIndexedConstraintName(&constraints[ref.offset], taken)
}

func prepareConstraintSymbols(table string, constraints []catalog.Constraint) error {
	seen := map[string]bool{}
	checkNumber, foreignNumber := 0, 0
	for index := range constraints {
		constraint := &constraints[index]
		if len(constraint.Columns) == 0 && constraint.Type != catalog.ConstraintTypeCheck {
			return sqlFailure{1064, "42000", "constraint requires columns"}
		}
		if unnamedUnique(*constraint) {
			continue
		}
		checkNumber, foreignNumber = assignConstraintSymbol(table, constraint, checkNumber, foreignNumber)
		if err := recordConstraintSymbol(seen, constraint.Name); err != nil {
			return err
		}
	}
	return nil
}

func unnamedUnique(constraint catalog.Constraint) bool {
	return constraint.Type == catalog.ConstraintTypeUnique && constraint.Name == ""
}

func assignConstraintSymbol(table string, constraint *catalog.Constraint, checkNumber, foreignNumber int) (int, int) {
	if constraint.Type == catalog.ConstraintTypePrimary {
		constraint.Name = "PRIMARY"
	}
	if constraint.Type == catalog.ConstraintTypeCheck && constraint.Name == "" {
		checkNumber++
		constraint.Name = fmt.Sprintf("%s_chk_%d", table, checkNumber)
	}
	if constraint.Type == catalog.ConstraintTypeForeignKey && constraint.Name == "" {
		foreignNumber++
		constraint.Name = fmt.Sprintf("%s_ibfk_%d", table, foreignNumber)
	}
	return checkNumber, foreignNumber
}

func recordConstraintSymbol(seen map[string]bool, name string) error {
	key := catalog.Key(name)
	if seen[key] {
		return sqlFailure{1061, "42000", "duplicate constraint name '" + name + "'"}
	}
	seen[key] = true
	return nil
}

func assignIndexedConstraintName(constraint *catalog.Constraint, taken map[string]bool) error {
	if constraint.Type == catalog.ConstraintTypePrimary {
		constraint.Name = "PRIMARY"
	}
	if unnamedUnique(*constraint) {
		constraint.Name = availableIndexName(constraint.Columns[0], taken)
	}
	key := catalog.Key(constraint.Name)
	if taken[key] {
		return sqlFailure{1061, "42000", "duplicate constraint name '" + constraint.Name + "'"}
	}
	taken[key] = true
	return nil
}

func isIndexedConstraint(constraint catalog.Constraint) bool {
	return constraint.Type == catalog.ConstraintTypePrimary || constraint.Type == catalog.ConstraintTypeUnique
}

func constraintSymbolKey(constraint catalog.Constraint) string {
	return constraintNameNamespace(constraint.Type) + "\x00" + catalog.Key(constraint.Name)
}

func constraintNameNamespace(constraintType string) string {
	if isIndexedConstraint(catalog.Constraint{Type: constraintType}) {
		return "key"
	}
	return constraintType
}

func rejectMultiplePrimaryKeys(constraints []catalog.Constraint) error {
	primary := 0
	for _, constraint := range constraints {
		if constraint.Type == catalog.ConstraintTypePrimary {
			primary++
		}
	}
	if primary > 1 {
		return sqlFailure{1068, "42000", "multiple primary keys"}
	}
	return nil
}

func copyKeyNames(names map[string]bool) map[string]bool {
	taken := make(map[string]bool, len(names))
	for name, present := range names {
		if present {
			taken[name] = true
		}
	}
	return taken
}
