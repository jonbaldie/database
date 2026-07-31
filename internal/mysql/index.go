package mysql

import (
	"strconv"
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

const (
	maxTableIndexes  = 64
	maxIndexParts    = 16
	maxIndexKeyWidth = 3072
)

func isTableIndexDefinition(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return indexDefinitionKeyword(lower, "index") || indexDefinitionKeyword(lower, "key") || indexDefinitionKeyword(lower, "unique index") || indexDefinitionKeyword(lower, "unique key") || indexDefinitionKeyword(lower, "fulltext") || indexDefinitionKeyword(lower, "spatial")
}

func indexDefinitionKeyword(value, keyword string) bool {
	return strings.HasPrefix(value, keyword) && wordEnd(value, len(keyword))
}

func parseTableIndexDefinition(value string) (catalog.Index, error) {
	unique, body, supported, matched := parseTableIndexHead(value)
	if !matched {
		return catalog.Index{}, sqlFailure{1064, "42000", "invalid index definition"}
	}
	if !supported {
		return catalog.Index{}, sqlFailure{1235, "42000", "unsupported index type"}
	}
	name, body, err := parseTableIndexName(body)
	if err != nil {
		return catalog.Index{}, err
	}
	parts, body, err := parseTableIndexParts(body)
	if err != nil {
		return catalog.Index{}, err
	}
	index := catalog.Index{Name: name, Unique: unique, Parts: parts}
	if err := parseTableIndexOptions(body, &index); err != nil {
		return catalog.Index{}, err
	}
	return index, nil
}

func parseTableIndexHead(value string) (bool, string, bool, bool) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if indexDefinitionKeyword(lower, "fulltext") || indexDefinitionKeyword(lower, "spatial") {
		return false, "", false, true
	}
	if indexDefinitionKeyword(lower, "unique index") {
		return true, strings.TrimSpace(value[len("UNIQUE INDEX"):]), true, true
	}
	if indexDefinitionKeyword(lower, "unique key") {
		return true, strings.TrimSpace(value[len("UNIQUE KEY"):]), true, true
	}
	if indexDefinitionKeyword(lower, "index") {
		return false, strings.TrimSpace(value[len("INDEX"):]), true, true
	}
	if indexDefinitionKeyword(lower, "key") {
		return false, strings.TrimSpace(value[len("KEY"):]), true, true
	}
	return false, "", false, false
}

func parseTableIndexName(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "(") {
		return "", value, nil
	}
	name, remainder, valid := consumeIdentifier(value)
	if !valid {
		return "", "", sqlFailure{1064, "42000", "invalid index name"}
	}
	if err := validateIdentifierLength(name); err != nil {
		return "", "", err
	}
	return name, strings.TrimSpace(remainder), nil
}

func parseTableIndexParts(value string) ([]catalog.IndexPart, string, error) {
	body, remainder, valid := consumeParenthesized(value)
	if !valid {
		return nil, "", sqlFailure{1064, "42000", "index requires key parts"}
	}
	parts := splitCSV(body)
	if len(parts) == 0 || len(parts) > maxIndexParts {
		return nil, "", sqlFailure{1069, "42000", "too many key parts"}
	}
	result := make([]catalog.IndexPart, len(parts))
	for number, part := range parts {
		parsed, err := parseTableIndexPart(part)
		if err != nil {
			return nil, "", err
		}
		result[number] = parsed
	}
	return result, strings.TrimSpace(remainder), nil
}

func parseTableIndexPart(value string) (catalog.IndexPart, error) {
	value, descending := trimIndexPartDirection(value)
	if strings.HasPrefix(value, "(") {
		expression, remainder, valid := consumeParenthesized(value)
		if !valid || strings.TrimSpace(remainder) != "" || strings.TrimSpace(expression) == "" {
			return catalog.IndexPart{}, sqlFailure{1064, "42000", "invalid functional index part"}
		}
		return catalog.IndexPart{Expression: expression, Descending: descending}, nil
	}
	column, remainder, valid := consumeIndexPartColumn(value)
	if !valid {
		return catalog.IndexPart{}, sqlFailure{1064, "42000", "invalid index key part"}
	}
	prefix, err := parseIndexPrefixLength(remainder)
	if err != nil {
		return catalog.IndexPart{}, err
	}
	return catalog.IndexPart{Column: column, PrefixLength: prefix, Descending: descending}, nil
}

func consumeIndexPartColumn(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if value[0] == '`' {
		token, remainder, valid := quotedIdentifierToken(value)
		if !valid {
			return "", "", false
		}
		column, valid := singleIdentifier(token)
		return column, remainder, valid
	}
	end := len(value)
	for number := range value {
		if value[number] == '(' || isRelationSpace(value[number]) {
			end = number
			break
		}
	}
	column, valid := singleIdentifier(value[:end])
	return column, value[end:], valid
}

func trimIndexPartDirection(value string) (string, bool) {
	value = strings.TrimSpace(value)
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return value, false
	}
	direction := strings.ToLower(fields[len(fields)-1])
	if direction != "asc" && direction != "desc" {
		return value, false
	}
	end := strings.LastIndex(strings.ToLower(value), direction)
	return strings.TrimSpace(value[:end]), direction == "desc"
}

func parseIndexPrefixLength(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	body, remainder, valid := consumeParenthesized(value)
	if !valid || strings.TrimSpace(remainder) != "" {
		return 0, sqlFailure{1064, "42000", "invalid index key part"}
	}
	prefix, err := strconv.Atoi(strings.TrimSpace(body))
	if err != nil || prefix < 1 {
		return 0, sqlFailure{1064, "42000", "invalid index prefix length"}
	}
	return prefix, nil
}

func parseTableIndexOptions(value string, index *catalog.Index) error {
	visibilitySet := false
	for strings.TrimSpace(value) != "" {
		next, err := parseNextTableIndexOption(value, index, &visibilitySet)
		if err != nil {
			return err
		}
		value = next
	}
	return nil
}

func parseNextTableIndexOption(value string, index *catalog.Index, visibilitySet *bool) (string, error) {
	if next, handled, err := parseIndexVisibilityOption(value, index, visibilitySet); handled || err != nil {
		return next, err
	}
	if next, handled, err := parseIndexMethodOption(value); handled || err != nil {
		return next, err
	}
	return parseIndexCommentOrUnsupported(value, index)
}

func parseIndexVisibilityOption(value string, index *catalog.Index, visibilitySet *bool) (string, bool, error) {
	next, set, invisible, err := parseIndexVisibility(value)
	if err != nil || !set {
		return next, set, err
	}
	if *visibilitySet {
		return "", true, sqlFailure{1064, "42000", "duplicate index visibility"}
	}
	*visibilitySet = true
	index.Invisible = invisible
	return next, true, nil
}

func parseIndexCommentOrUnsupported(value string, index *catalog.Index) (string, error) {
	next, comment, handled, err := parseIndexCommentOption(value)
	if err != nil {
		return "", err
	}
	if !handled {
		return "", sqlFailure{1235, "42000", "unsupported index option"}
	}
	index.Comment = comment
	return next, nil
}

func parseIndexVisibility(value string) (string, bool, bool, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if indexDefinitionKeyword(lower, "visible") {
		return value[len("VISIBLE"):], true, false, nil
	}
	if indexDefinitionKeyword(lower, "invisible") {
		return value[len("INVISIBLE"):], true, true, nil
	}
	return value, false, false, nil
}

func parseIndexMethodOption(value string) (string, bool, error) {
	if !indexDefinitionKeyword(strings.ToLower(strings.TrimSpace(value)), "using") {
		return value, false, nil
	}
	next, err := parseIndexUsing(value)
	return next, true, err
}

func parseIndexCommentOption(value string) (string, string, bool, error) {
	if !indexDefinitionKeyword(strings.ToLower(strings.TrimSpace(value)), "comment") {
		return value, "", false, nil
	}
	next, comment, err := parseIndexComment(value)
	return next, comment, true, err
}

func parseIndexUsing(value string) (string, error) {
	value = strings.TrimSpace(value[len("USING"):])
	method, remainder, valid := consumeIdentifier(value)
	if !valid || !strings.EqualFold(method, "btree") {
		return "", sqlFailure{1235, "42000", "only BTREE indexes are supported"}
	}
	return remainder, nil
}

func parseIndexComment(value string) (string, string, error) {
	value = strings.TrimSpace(value[len("COMMENT"):])
	comment, remainder, valid := consumeConstraintValue(value)
	if !valid {
		return "", "", sqlFailure{1064, "42000", "invalid index comment"}
	}
	tokens, err := tokenizeExpression(comment)
	if err != nil || len(tokens) != 1 || tokens[0].kind != tokenString {
		return "", "", sqlFailure{1064, "42000", "index comment requires a string literal"}
	}
	return remainder, tokens[0].str, nil
}

func namedTableIndexes(tableName string, indexes []catalog.Index, constraints []catalog.Constraint) ([]catalog.Index, error) {
	result := catalog.CloneIndexes(indexes)
	taken := tableConstraintIndexNames(constraints)
	for number := range result {
		index := &result[number]
		if index.Name == "" {
			index.Name = availableIndexName(indexNameBase(tableName, *index), taken)
		}
		key := catalog.Key(index.Name)
		if taken[key] {
			return nil, sqlFailure{1061, "42000", "duplicate key name '" + index.Name + "'"}
		}
		taken[key] = true
	}
	return result, nil
}

func tableConstraintIndexNames(constraints []catalog.Constraint) map[string]bool {
	taken := map[string]bool{}
	for _, constraint := range constraints {
		if constraint.Type == catalog.ConstraintTypePrimary || constraint.Type == catalog.ConstraintTypeUnique {
			taken[catalog.Key(constraint.Name)] = true
		}
	}
	return taken
}

func indexNameBase(tableName string, index catalog.Index) string {
	if len(index.Parts) == 0 {
		return tableName + "_index"
	}
	if index.Parts[0].Column != "" {
		return index.Parts[0].Column
	}
	return tableName + "_functional"
}

func availableIndexName(base string, taken map[string]bool) string {
	if !taken[catalog.Key(base)] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := base + "_" + strconv.Itoa(suffix)
		if !taken[catalog.Key(candidate)] {
			return candidate
		}
	}
}

func effectiveTableIndexes(table catalog.Table) []catalog.Index {
	indexes := catalog.CloneIndexes(table.Indexes)
	for _, constraint := range table.Constraints {
		if constraint.Type != catalog.ConstraintTypePrimary && constraint.Type != catalog.ConstraintTypeUnique {
			continue
		}
		parts := make([]catalog.IndexPart, len(constraint.Columns))
		for number, column := range constraint.Columns {
			parts[number].Column = column
		}
		indexes = append(indexes, catalog.Index{Name: constraint.Name, Unique: true, Parts: parts})
	}
	return indexes
}

func validateTableIndexes(table catalog.Table) error {
	indexes := effectiveTableIndexes(table)
	if len(indexes) > maxTableIndexes {
		return sqlFailure{1069, "42000", "too many keys"}
	}
	seen := map[string]bool{}
	for _, index := range indexes {
		if err := validateTableIndex(table, index, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateTableIndex(table catalog.Table, index catalog.Index, seen map[string]bool) error {
	if index.Name == "" || validateIdentifierLength(index.Name) != nil {
		return sqlFailure{1064, "42000", "invalid index name"}
	}
	key := catalog.Key(index.Name)
	if seen[key] {
		return sqlFailure{1061, "42000", "duplicate key name '" + index.Name + "'"}
	}
	seen[key] = true
	if len(index.Parts) == 0 || len(index.Parts) > maxIndexParts {
		return sqlFailure{1069, "42000", "invalid key part count"}
	}
	return validateTableIndexParts(table, index)
}

func validateTableIndexParts(table catalog.Table, index catalog.Index) error {
	columns, err := tableColumnIndexes(table)
	if err != nil {
		return err
	}
	width := 0
	for _, part := range index.Parts {
		partWidth, err := validateTableIndexPart(table, columns, part)
		if err != nil {
			return err
		}
		width += partWidth
	}
	if width > maxIndexKeyWidth {
		return sqlFailure{1071, "42000", "specified key was too long"}
	}
	return nil
}

func validateTableIndexPart(table catalog.Table, columns map[string]int, part catalog.IndexPart) (int, error) {
	if (part.Column == "") == (part.Expression == "") {
		return 0, sqlFailure{1064, "42000", "index key part requires one column or expression"}
	}
	if part.Column != "" {
		column, found := columns[catalog.Key(part.Column)]
		if !found {
			return 0, sqlFailure{1072, "42000", "key column '" + part.Column + "' doesn't exist in table"}
		}
		return indexColumnWidth(table, column, part.PrefixLength)
	}
	if part.PrefixLength != 0 {
		return 0, sqlFailure{1235, "42000", "functional index prefixes are unsupported"}
	}
	metadata, err := relationExpressionMetadata(part.Expression, relationalTableColumns("", table.Name, table.Name, table))
	if err != nil {
		return 0, err
	}
	return int(metadata.length), nil
}

func indexColumnWidth(table catalog.Table, column, prefix int) (int, error) {
	typeName, found := table.ColumnType(column)
	if !found {
		return 0, sqlFailure{1235, "42000", "indexing a legacy column type is unsupported"}
	}
	character, err := parseCharacterType(typeName)
	if err != nil {
		return 0, err
	}
	if character.kind != characterNone {
		return indexCharacterWidth(character, prefix)
	}
	if prefix != 0 {
		return 0, sqlFailure{1089, "HY000", "incorrect prefix key"}
	}
	if numeric, err := parseNumericType(typeName); err != nil {
		return 0, err
	} else if numeric.kind != numericNone {
		return indexNumericWidth(numeric), nil
	}
	if temporal, err := parseTemporalType(typeName); err != nil {
		return 0, err
	} else if temporal.kind != temporalNone {
		return int(temporal.length), nil
	}
	return 0, sqlFailure{1235, "42000", "unsupported index key type"}
}

func indexCharacterWidth(character characterType, prefix int) (int, error) {
	length := character.length
	if !character.bounded && prefix == 0 {
		return 0, sqlFailure{1170, "42000", "BLOB/TEXT column used in key specification without a key length"}
	}
	if prefix > 0 {
		if character.bounded && prefix > length {
			return 0, sqlFailure{1089, "HY000", "incorrect prefix key"}
		}
		length = prefix
	}
	if character.kind == characterText {
		return length * 4, nil
	}
	return length, nil
}

func indexNumericWidth(numeric numericType) int {
	switch numeric.kind {
	case numericDecimal:
		return (numeric.precision + 1) / 2
	case numericBit:
		return (numeric.width + 7) / 8
	case numericBoolean:
		return 1
	default:
		return 8
	}
}
