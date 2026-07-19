package mysql

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func relationalSelectExecutor(t *testing.T) *textStatementExecutor {
	t.Helper()
	store, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateNamespace("app"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTableWithTypes("app", "authors", []string{"id", "name"}, []string{"INT", "VARCHAR(32)"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTableWithTypes("app", "posts", []string{"id", "author_id", "title", "score"}, []string{"INT", "INT", "VARCHAR(32)", "INT"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTableWithTypes("app", "author_labels", []string{"id", "label"}, []string{"INT", "VARCHAR(32)"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]string{{"1", "Ada"}, {"2", "Grace"}, {"3", "Linus"}} {
		if err := store.Insert("app", "authors", row); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]string{{"10", "1", "first", "5"}, {"11", "1", "second", "20"}, {"12", "2", "third", "15"}} {
		if err := store.Insert("app", "posts", row); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]string{{"1", "A"}, {"2", "G"}} {
		if err := store.Insert("app", "author_labels", row); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	session := &session{server: server, database: "app", initialDB: "app", timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{}}
	return &textStatementExecutor{session}
}

func TestRelationalSelectUsingCoalescesColumns(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT * FROM authors a LEFT JOIN author_labels l USING (id) ORDER BY id")
	if err != nil {
		t.Fatalf("using join: %v", err)
	}
	wantColumns := []string{"id", "name", "label"}
	wantRows := [][]string{{"1", "Ada", "A"}, {"2", "Grace", "G"}, {"3", "Linus", "NULL"}}
	if !reflect.DeepEqual(result.columns, wantColumns) || !reflect.DeepEqual(result.rows, wantRows) {
		t.Fatalf("columns/rows = %#v/%#v, want %#v/%#v", result.columns, result.rows, wantColumns, wantRows)
	}

	result, err = executor.execute("SELECT * FROM author_labels l RIGHT JOIN authors a USING (id) ORDER BY id")
	if err != nil {
		t.Fatalf("right using join: %v", err)
	}
	rightColumns := []string{"id", "label", "name"}
	rightRows := [][]string{{"1", "A", "Ada"}, {"2", "G", "Grace"}, {"3", "NULL", "Linus"}}
	if !reflect.DeepEqual(result.columns, rightColumns) || !reflect.DeepEqual(result.rows, rightRows) {
		t.Fatalf("right columns/rows = %#v/%#v, want %#v/%#v", result.columns, result.rows, rightColumns, rightRows)
	}
}

func TestRelationalSelectJoinsAndOrderedShape(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT a.name, p.title FROM authors AS a INNER JOIN posts AS p ON a.id = p.author_id WHERE p.score >= 10 ORDER BY p.id DESC LIMIT 2")
	if err != nil {
		t.Fatalf("join select: %v", err)
	}
	if !slices.Equal(result.columns, []string{"name", "title"}) {
		t.Fatalf("columns = %q", result.columns)
	}
	want := [][]string{{"Grace", "third"}, {"Ada", "second"}}
	if !reflect.DeepEqual(result.rows, want) {
		t.Fatalf("rows = %#v, want %#v", result.rows, want)
	}
	if result.metadata[0].table != "authors" || result.metadata[1].table != "posts" {
		t.Fatalf("metadata = %#v", result.metadata)
	}
}

func TestRelationalSelectOuterJoinDistinctAndNulls(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT DISTINCT a.name, p.title FROM authors a LEFT JOIN posts p ON a.id = p.author_id ORDER BY a.id ASC")
	if err != nil {
		t.Fatalf("left join: %v", err)
	}
	want := [][]string{{"Ada", "first"}, {"Ada", "second"}, {"Grace", "third"}, {"Linus", "NULL"}}
	if !reflect.DeepEqual(result.rows, want) || !reflect.DeepEqual(result.nulls, [][]bool{{false, false}, {false, false}, {false, false}, {false, true}}) {
		t.Fatalf("rows/nulls = %#v/%#v", result.rows, result.nulls)
	}
}

func TestRelationalSelectExplanationTracesOperators(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("EXPLAIN FORMAT=JSON SELECT DISTINCT a.name FROM authors a JOIN posts p ON a.id = p.author_id ORDER BY a.name DESC LIMIT 1")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		kinds = append(kinds, node["kind"].(string))
		for _, child := range node["children"].([]any) {
			visit(child.(map[string]any))
		}
	}
	visit(document["plan"].(map[string]any))
	for _, kind := range []string{"limit", "sort", "distinct", "project", "join", "scan"} {
		if !slices.Contains(kinds, kind) {
			t.Errorf("operator kinds %q missing %q", kinds, kind)
		}
	}
}
