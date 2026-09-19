package mysql

import "strconv"

func isShowCharacterSetStatement(lower string) bool {
	return isShowPrefix(lower, "show character set") || isShowPrefix(lower, "show charset")
}

func isShowCollationStatement(lower string) bool {
	return isShowPrefix(lower, "show collation")
}

func (s *catalogExecutor) showCharacterSetStatement(query, lower string) (*queryResult, error) {
	remainder, ok := cutShowPrefix(query, lower, "show character set")
	if !ok {
		remainder, ok = cutShowPrefix(query, lower, "show charset")
	}
	if !ok {
		return nil, sqlFailure{1064, "42000", "unsupported query"}
	}
	modifiers, err := parseShowModifiers(remainder, false)
	if err != nil {
		return nil, err
	}
	return applyShowFilters(s.session, showCharacterSet(), modifiers)
}

func (s *catalogExecutor) showCollationStatement(query, lower string) (*queryResult, error) {
	remainder, ok := cutShowPrefix(query, lower, "show collation")
	if !ok {
		return nil, sqlFailure{1064, "42000", "unsupported query"}
	}
	modifiers, err := parseShowModifiers(remainder, false)
	if err != nil {
		return nil, err
	}
	return applyShowFilters(s.session, showCollation(), modifiers)
}

func showCharacterSet() *queryResult {
	return &queryResult{
		columns: []string{"Charset", "Description", "Default collation", "Maxlen"},
		rows:    [][]string{{"utf8mb4", "UTF-8 Unicode", "utf8mb4_0900_ai_ci", "4"}},
	}
}

func showCollation() *queryResult {
	return &queryResult{
		columns: []string{"Collation", "Charset", "Id", "Default", "Compiled", "Sortlen", "Pad_attribute"},
		rows: [][]string{
			{"utf8mb4_0900_ai_ci", "utf8mb4", strconv.Itoa(int(mysqlCharsetUTF8MB40900AICI)), "Yes", "Yes", "0", "NO PAD"},
			{"utf8mb4_bin", "utf8mb4", strconv.Itoa(int(mysqlCharsetUTF8MB4Bin)), "", "Yes", "1", "PAD SPACE"},
		},
	}
}
