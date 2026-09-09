package catalog

import (
	"testing"
)

// A schema change rewrites the durable row image: the write-ahead log keeps
// historical records at the previous column width, so a restart must replay
// from the snapshot taken at the commit instead of the raw log.
func TestOpenAfterSchemaChangeReplaysDurableRows(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(Definition) (Definition, error)
	}{
		{
			name: "drop_column",
			mutate: func(definition Definition) (Definition, error) {
				table := definition.Namespaces["app"].Tables["items"]
				table.Columns = []string{"id", "value"}
				table.ColumnTypes = table.ColumnTypes[:2]
				rows := make([][]string, len(table.Rows))
				for index, row := range table.Rows {
					rows[index] = []string{row[0], row[2]}
				}
				table.Rows = rows
				table.Indexes = nil
				definition.Namespaces["app"].Tables["items"] = table
				return definition, nil
			},
		},
		{
			name: "add_column",
			mutate: func(definition Definition) (Definition, error) {
				table := definition.Namespaces["app"].Tables["items"]
				table.Columns = append(table.Columns, "extra")
				table.ColumnTypes = append(table.ColumnTypes, "INT")
				rows := make([][]string, len(table.Rows))
				for index, row := range table.Rows {
					rows[index] = append(append([]string(nil), row...), "NULL")
				}
				table.Rows = rows
				definition.Namespaces["app"].Tables["items"] = table
				return definition, nil
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			store, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CreateNamespace("app"); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateTableWithTypes("app", "items", []string{"id", "name", "value"}, []string{"INT", "VARCHAR(20)", "INT"}); err != nil {
				t.Fatal(err)
			}
			if err := store.Insert("app", "items", []string{"1", "alpha", "10"}); err != nil {
				t.Fatal(err)
			}
			if err := store.Insert("app", "items", []string{"2", "beta", "20"}); err != nil {
				t.Fatal(err)
			}
			if err := store.ApplyDurable(test.mutate); err != nil {
				t.Fatalf("apply schema change: %v", err)
			}
			if err := store.Rows().Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := Open(directory)
			if err != nil {
				t.Fatalf("reopen after schema change: %v", err)
			}
			defer reopened.Rows().Close()
			snapshot := reopened.Snapshot()
			table := snapshot.Namespaces["app"].Tables["items"]
			want := [][]string{{"1", "10"}, {"2", "20"}}
			if test.name == "add_column" {
				want = [][]string{{"1", "alpha", "10", "NULL"}, {"2", "beta", "20", "NULL"}}
			}
			if !sameRows(table.Rows, want) {
				t.Fatalf("rows after reopen = %#v, want %#v", table.Rows, want)
			}
		})
	}
}

func sameRows(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if len(left[index]) != len(right[index]) {
			return false
		}
		for position := range left[index] {
			if left[index][position] != right[index][position] {
				return false
			}
		}
	}
	return true
}
