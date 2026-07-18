package queryexplanation

import (
	"strconv"
	"strings"
)

// Tabular is the stable MySQL-oriented projection of an explanation document.
// Null marks the cells that are SQL NULL in the emitted result set.
type Tabular struct {
	Columns []string
	Rows    [][]string
	Null    [][]bool
}

// TabularColumns is the stable ordered column prefix of the tabular projection.
var TabularColumns = []string{
	"id", "select_type", "table", "partitions", "type", "possible_keys", "key",
	"key_len", "ref", "rows", "filtered", "Extra",
	"operator_id", "parent_operator_id", "operator", "strategy", "estimated_cost",
	"estimated_memory_bytes", "actual_rows", "loops", "first_row_ms", "total_ms",
	"summary", "warnings",
}

type cell struct {
	value string
	null  bool
}

func text(value string) cell { return cell{value: value} }

func nullCell() cell { return cell{null: true} }

// RenderTabular projects a plan document onto the stable tabular columns.
func RenderTabular(document *Document) Tabular {
	tabular := Tabular{Columns: append([]string(nil), TabularColumns...)}
	appendOperatorRows(&tabular, document.Plan, 0)
	return tabular
}

func appendOperatorRows(tabular *Tabular, operator *Operator, parentID int) {
	cells := operatorCells(operator, parentID)
	row := make([]string, len(cells))
	nulls := make([]bool, len(cells))
	for index, entry := range cells {
		row[index], nulls[index] = entry.value, entry.null
	}
	tabular.Rows = append(tabular.Rows, row)
	tabular.Null = append(tabular.Null, nulls)
	for _, child := range operator.Children {
		appendOperatorRows(tabular, child, operator.ID)
	}
}

func operatorCells(operator *Operator, parentID int) []cell {
	cells := mysqlPrefixCells(operator)
	return append(cells, stableCells(operator, parentID)...)
}

func mysqlPrefixCells(operator *Operator) []cell {
	return []cell{
		text(strconv.Itoa(operator.ID)),
		text("SIMPLE"),
		tableCell(operator),
		nullCell(),
		accessTypeCell(operator),
		nullCell(),
		nullCell(),
		nullCell(),
		nullCell(),
		text(formatRows(operator.Estimates.Rows)),
		text("100.00"),
		nullCell(),
	}
}

func stableCells(operator *Operator, parentID int) []cell {
	return []cell{
		text(strconv.Itoa(operator.ID)),
		parentCell(parentID),
		text(operator.Kind),
		strategyCell(operator),
		text(formatNumber(operator.Estimates.Cost)),
		text(strconv.Itoa(operator.Estimates.PeakMemoryBytes)),
		nullCell(),
		nullCell(),
		nullCell(),
		nullCell(),
		text(operator.Summary),
		text(warningCodes(operator.Warnings)),
	}
}

func parentCell(parentID int) cell {
	if parentID == 0 {
		return nullCell()
	}
	return text(strconv.Itoa(parentID))
}

func strategyCell(operator *Operator) cell {
	if operator.Strategy == nil {
		return nullCell()
	}
	return text(operator.Strategy.Name)
}

func accessTypeCell(operator *Operator) cell {
	if operator.Kind == "scan" {
		return text("ALL")
	}
	return nullCell()
}

func tableCell(operator *Operator) cell {
	for _, object := range operator.Objects {
		if object.Type == "table" {
			return text(object.Name)
		}
	}
	return nullCell()
}

func warningCodes(warnings []Warning) string {
	codes := make([]string, len(warnings))
	for index, warning := range warnings {
		codes[index] = warning.Code
	}
	return strings.Join(codes, ";")
}

func formatRows(rows float64) string {
	return strconv.FormatFloat(rows, 'f', -1, 64)
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
