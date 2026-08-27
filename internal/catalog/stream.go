package catalog

import (
	"encoding/json"
	"io"
	"sort"
)

// Write encodes a catalog without buffering all table rows in one byte slice.
func Write(writer io.Writer, definition Definition) error {
	if _, err := io.WriteString(writer, `{"namespaces":`); err != nil {
		return err
	}
	if err := writeCatalogNamespaces(writer, definition.Namespaces); err != nil {
		return err
	}
	if len(definition.Accounts) > 0 {
		if _, err := io.WriteString(writer, `,"accounts":`); err != nil {
			return err
		}
		if err := writeCatalogJSON(writer, definition.Accounts); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "}\n")
	return err
}

func writeCatalogNamespaces(writer io.Writer, namespaces map[string]Namespace) error {
	if _, err := io.WriteString(writer, "{"); err != nil {
		return err
	}
	keys := sortedCatalogKeys(namespaces)
	for index, key := range keys {
		if err := writeCatalogMapPrefix(writer, key, index > 0); err != nil {
			return err
		}
		if err := writeCatalogNamespace(writer, namespaces[key]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "}")
	return err
}

func writeCatalogNamespace(writer io.Writer, namespace Namespace) error {
	if _, err := io.WriteString(writer, "{"); err != nil {
		return err
	}
	separator := false
	if namespace.Name != "" {
		if _, err := io.WriteString(writer, `"name":`); err != nil {
			return err
		}
		if err := writeCatalogJSON(writer, namespace.Name); err != nil {
			return err
		}
		separator = true
	}
	if separator {
		if _, err := io.WriteString(writer, ","); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, `"tables":`); err != nil {
		return err
	}
	if err := writeCatalogTables(writer, namespace.Tables); err != nil {
		return err
	}
	_, err := io.WriteString(writer, "}")
	return err
}

func writeCatalogTables(writer io.Writer, tables map[string]Table) error {
	if _, err := io.WriteString(writer, "{"); err != nil {
		return err
	}
	keys := sortedCatalogKeys(tables)
	for index, key := range keys {
		if err := writeCatalogMapPrefix(writer, key, index > 0); err != nil {
			return err
		}
		if err := writeCatalogTable(writer, tables[key]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "}")
	return err
}

func writeCatalogTable(writer io.Writer, table Table) error {
	rows := table.Rows
	table.Rows = nil
	encoded, err := json.Marshal(table)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		_, err = writer.Write(encoded)
		return err
	}
	return writeCatalogTableRows(writer, encoded, rows)
}

func writeCatalogTableRows(writer io.Writer, encoded []byte, rows [][]string) error {
	if _, err := writer.Write(encoded[:len(encoded)-1]); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, `,"rows":[`); err != nil {
		return err
	}
	for index, row := range rows {
		if index > 0 {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		if err := writeCatalogJSON(writer, row); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "]}")
	return err
}

func writeCatalogMapPrefix(writer io.Writer, key string, separator bool) error {
	if separator {
		if _, err := io.WriteString(writer, ","); err != nil {
			return err
		}
	}
	if err := writeCatalogJSON(writer, key); err != nil {
		return err
	}
	_, err := io.WriteString(writer, ":")
	return err
}

func writeCatalogJSON(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(encoded)
	return err
}

func sortedCatalogKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
