package mysql

import (
	"strconv"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func BenchmarkValidateForeignKeyRows(b *testing.B) {
	for _, rows := range []int{100, 400, 800} {
		b.Run("child_and_parent_rows="+strconv.Itoa(rows), func(b *testing.B) {
			child, parent := foreignKeyBenchmarkTables(rows)
			constraint := catalog.Constraint{
				Name:              "child_parent",
				Type:              catalog.ConstraintTypeForeignKey,
				Columns:           []string{"parent_id"},
				ReferencedTable:   "parent",
				ReferencedColumns: []string{"id"},
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := validateForeignKeyRows(catalog.Definition{}, "app", "child", child, parent, constraint, []int{1}, []int{0}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func foreignKeyBenchmarkTables(rows int) (catalog.Table, catalog.Table) {
	parent := catalog.Table{Columns: []string{"id"}, ColumnTypes: []string{"INT"}, Rows: make([][]string, rows)}
	for row := range rows {
		parent.Rows[row] = []string{strconv.Itoa(row)}
	}
	child := catalog.Table{Columns: []string{"id", "parent_id"}, ColumnTypes: []string{"INT", "INT"}, Rows: make([][]string, rows)}
	parentKey := strconv.Itoa(rows - 1)
	for row := range rows {
		child.Rows[row] = []string{strconv.Itoa(row), parentKey}
	}
	return child, parent
}
