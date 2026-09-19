package mysql

import (
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

func isShowTablesStatement(lower string) bool {
	return isShowPrefix(lower, "show tables") || isShowPrefix(lower, "show full tables")
}

func (s *catalogExecutor) showTablesStatement(query, lower string) (*queryResult, error) {
	full, remainder, ok := cutShowTables(query, lower)
	if !ok {
		return nil, sqlFailure{1064, "42000", "unsupported query"}
	}
	modifiers, err := parseShowModifiers(remainder, false)
	if err != nil {
		return nil, err
	}
	result, err := s.showTablesIn(modifiers.namespace, full)
	if err != nil {
		return nil, err
	}
	return applyShowFilters(s.session, result, modifiers)
}

func cutShowTables(query, lower string) (bool, string, bool) {
	if remainder, ok := cutShowPrefix(query, lower, "show full tables"); ok {
		return true, remainder, true
	}
	if remainder, ok := cutShowPrefix(query, lower, "show tables"); ok {
		return false, remainder, true
	}
	return false, "", false
}

func (s *catalogExecutor) showTables() (*queryResult, error) {
	return s.showTablesIn("", false)
}

func (s *catalogExecutor) showTablesIn(name string, full bool) (*queryResult, error) {
	if name == "" {
		name = s.database
	}
	if name == "" {
		return nil, sqlFailure{1046, "3D000", "no database selected"}
	}
	if strings.EqualFold(name, informationSchemaName) {
		result := informationSchemaTables()
		if full {
			return withShowTableType(result, "SYSTEM VIEW"), nil
		}
		return result, nil
	}
	namespace, found := s.metadataDefinition().Namespaces[catalog.Key(name)]
	if !found {
		return nil, metadataNamespaceFailure(s.server.config.Catalog, name)
	}
	display := namespace.Name
	if display == "" {
		display = name
	}
	result := namespaceTables(display, namespace)
	if full {
		return withShowTableType(result, "BASE TABLE"), nil
	}
	return result, nil
}

func withShowTableType(result *queryResult, tableType string) *queryResult {
	if result == nil {
		return result
	}
	result.columns = append(result.columns, "Table_type")
	for index, row := range result.rows {
		result.rows[index] = append(row, tableType)
	}
	for index, null := range result.nulls {
		result.nulls[index] = append(null, false)
	}
	return result
}
