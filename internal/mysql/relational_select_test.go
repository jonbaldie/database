package mysql

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
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
	if err := store.CreateTableWithTypes("app", "unicode_rows", []string{"café"}, []string{"INT"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTableWithTypes("app", "unicode_other", []string{"café"}, []string{"INT"}); err != nil {
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
	if err := store.Insert("app", "unicode_rows", []string{"7"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert("app", "unicode_other", []string{"7"}); err != nil {
		t.Fatal(err)
	}
	server, err := NewWithConfig("127.0.0.1:0", Config{Catalog: store, Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Listener.Close() })
	session := &session{server: server, database: "app", initialDB: "app", timeZone: "UTC", initialTimeZone: "UTC", statements: map[uint32]*preparedStatement{}}
	return &textStatementExecutor{session: session}
}

func TestRelationalSelectUsesCanonicalCaselessIdentifiers(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT café.café + 1 AS résumé FROM unicode_rows AS café ORDER BY résumé")
	if err != nil {
		t.Fatalf("canonical identifier query: %v", err)
	}
	if !reflect.DeepEqual(result.rows, [][]string{{"8"}}) {
		t.Fatalf("rows = %#v", result.rows)
	}

	result, err = executor.execute("SELECT café.* FROM unicode_rows AS café JOIN unicode_other AS other USING (café)")
	if err != nil {
		t.Fatalf("canonical wildcard/USING query: %v", err)
	}
	if !reflect.DeepEqual(result.rows, [][]string{{"7"}}) {
		t.Fatalf("wildcard rows = %#v", result.rows)
	}

	result, err = executor.execute("SELECT `café`.`café` + 1 AS `résumé` FROM unicode_rows AS `café` ORDER BY `résumé`")
	if err != nil {
		t.Fatalf("quoted computed identifier query: %v", err)
	}
	if !reflect.DeepEqual(result.rows, [][]string{{"8"}}) {
		t.Fatalf("quoted computed rows = %#v", result.rows)
	}

	result, err = executor.execute("SELECT `café`.* FROM unicode_rows AS `café`")
	if err != nil || !reflect.DeepEqual(result.rows, [][]string{{"7"}}) {
		t.Fatalf("quoted wildcard rows = %#v, err = %v", result.rows, err)
	}

	_, err = executor.execute("SELECT * FROM authors AS café JOIN posts AS café ON 1 = 1")
	if !isFailureCode(err, 1066) {
		t.Fatalf("canonical duplicate alias error = %v", err)
	}
}

func TestRelationalSelectRejectsOverlongAliases(t *testing.T) {
	executor := relationalSelectExecutor(t)
	alias := strings.Repeat("a", 65)
	for _, query := range []string{
		"SELECT * FROM authors AS " + alias,
		"SELECT id AS " + alias + " FROM authors",
	} {
		if _, err := executor.execute(query); !isFailureCode(err, 1059) {
			t.Errorf("execute(%q) error = %v", query, err)
		}
	}
}

func TestRelationalSelectExplanationTracesUsingClause(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("EXPLAIN FORMAT=JSON SELECT * FROM authors a JOIN author_labels l USING (id)")
	if err != nil {
		t.Fatal(err)
	}
	explanation := result.rows[0][0]
	if !strings.Contains(explanation, `"clause":"using"`) || !strings.Contains(explanation, `"fragment":"(id)"`) {
		t.Fatalf("USING source missing: %s", explanation)
	}
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

func TestRelationalSelectUsingPreservesQuotedIdentifiers(t *testing.T) {
	executor := relationalSelectExecutor(t)
	store := executor.server.config.Catalog
	for _, table := range []string{"quoted_left", "quoted_right"} {
		if err := store.CreateTableWithTypes("app", table, []string{"odd.name"}, []string{"INT"}); err != nil {
			t.Fatal(err)
		}
		if err := store.Insert("app", table, []string{"7"}); err != nil {
			t.Fatal(err)
		}
	}
	query := "SELECT * FROM quoted_left AS `l.alias` JOIN quoted_right AS `r``alias` USING (`odd.name`)"
	result, err := executor.execute(query)
	if err != nil {
		t.Fatalf("quoted USING: %v", err)
	}
	if !reflect.DeepEqual(result.columns, []string{"odd.name"}) || !reflect.DeepEqual(result.rows, [][]string{{"7"}}) {
		t.Fatalf("columns/rows = %#v/%#v", result.columns, result.rows)
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

func TestRelationalSelectEvaluatesRowBoundExpressions(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT p.score + 1 AS adjusted FROM posts p WHERE p.score * 2 >= 30 ORDER BY adjusted DESC")
	if err != nil {
		t.Fatalf("row expression: %v", err)
	}
	if !reflect.DeepEqual(result.rows, [][]string{{"21"}, {"16"}}) {
		t.Fatalf("rows = %#v", result.rows)
	}
}

func TestRelationalSelectOrdersByRowExpression(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT p.title FROM posts p ORDER BY p.score + 1 DESC")
	if err != nil {
		t.Fatalf("ORDER BY expression: %v", err)
	}
	if !reflect.DeepEqual(result.rows, [][]string{{"second"}, {"third"}, {"first"}}) {
		t.Fatalf("rows = %#v", result.rows)
	}

	if _, err := executor.execute("SELECT DISTINCT p.title FROM posts p ORDER BY p.score + 1"); !isFailureCode(err, 3065) {
		t.Fatalf("DISTINCT non-projected ORDER BY error = %v", err)
	}
}

func TestRelationalSelectComputedMetadataIsStableAndNullable(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT p.score + 1 AS adjusted FROM posts p")
	if err != nil {
		t.Fatal(err)
	}
	metadata := result.metadata[0]
	if metadata.typ != mysqlTypeLongLong || metadata.length != 20 || metadata.flags&mysqlNotNullFlag != 0 {
		t.Fatalf("computed metadata = %#v", metadata)
	}

	preparation := &preparedPreparation{executor.session}
	prepared, err := preparation.preparedColumns("SELECT p.score + ? AS adjusted FROM posts p")
	if err != nil || len(prepared) != 1 {
		t.Fatalf("prepared metadata = %#v, err = %v", prepared, err)
	}
	if prepared[0].typ != metadata.typ || prepared[0].length != metadata.length || prepared[0].flags&mysqlNotNullFlag != 0 {
		t.Fatalf("prepared metadata = %#v, text = %#v", prepared[0], metadata)
	}
}

func TestRelationalSelectComputedMetadataIgnoresSyntheticDomains(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT SQRT(p.score - 2) AS rooted FROM posts p WHERE p.score >= 5")
	if err != nil {
		t.Fatalf("domain-independent metadata: %v", err)
	}
	if result.metadata[0].typ != mysqlTypeDouble {
		t.Fatalf("SQRT metadata = %#v", result.metadata[0])
	}

	result, err = executor.execute("SELECT UPPER(a.name) AS upper_name FROM authors a JOIN posts p ON a.id = p.author_id")
	if err != nil {
		t.Fatal(err)
	}
	if result.metadata[0].length != 128 {
		t.Fatalf("UPPER metadata length = %d, want referenced VARCHAR length 128", result.metadata[0].length)
	}
}

func TestRelationalSelectPreparedMetadataSupportsMixedParameterDomains(t *testing.T) {
	executor := relationalSelectExecutor(t)
	preparation := &preparedPreparation{executor.session}
	metadata, err := preparation.preparedColumns("SELECT CONCAT(p.title, ?), p.score + ? FROM posts p")
	if err != nil || len(metadata) != 2 {
		t.Fatalf("mixed prepared metadata = %#v, err = %v", metadata, err)
	}
	if metadata[0].typ != mysqlTypeVarString || metadata[1].typ != mysqlTypeLongLong {
		t.Fatalf("mixed prepared metadata = %#v", metadata)
	}
}

func TestRelationalSelectAliasHidesOriginalTableName(t *testing.T) {
	executor := relationalSelectExecutor(t)
	if _, err := executor.execute("SELECT authors.name FROM authors AS a"); !isFailureCode(err, 1054) {
		t.Fatalf("hidden original table name error = %v", err)
	}
}

func TestRelationalSelectJoinOnCannotReferenceLaterTable(t *testing.T) {
	executor := relationalSelectExecutor(t)
	query := "SELECT a.name FROM authors a JOIN posts p ON l.id = a.id JOIN author_labels l ON l.id = a.id"
	if _, err := executor.execute(query); !isFailureCode(err, 1054) {
		t.Fatalf("later-table ON reference error = %v", err)
	}
}

func TestRelationalSelectStreamsFromStatementSnapshot(t *testing.T) {
	executor := relationalSelectExecutor(t)
	executor.streamRows = true
	result, err := executor.execute("SELECT id FROM authors WHERE id >= 1 LIMIT 2")
	if err != nil {
		t.Fatal(err)
	}
	if result.stream == nil || len(result.rows) != 0 {
		t.Fatalf("result was buffered: %#v", result)
	}
	if err := executor.server.config.Catalog.Insert("app", "authors", []string{"4", "Margaret"}); err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	if err := result.stream(func(row []string, _ []bool) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("streamed rows = %#v", rows)
	}
}

func TestRelationalSelectMemoryLimitAbortsOnlyTheStatement(t *testing.T) {
	executor := relationalSelectExecutor(t)
	config := Config{ResourceLimits: ResourceLimits{
		ExecutionMemoryLimitBytes:           7,
		AggregateExecutionMemoryLimitBytes:  7,
		TemporaryStorageLimitBytes:          1024,
		AggregateTemporaryStorageLimitBytes: 1024,
	}}
	resources := newStatementResources(newResourceManager(config), config, nil)
	executor.session.resources = resources
	if _, err := executor.execute("SELECT name FROM authors"); !isFailureCode(err, 1114) {
		t.Fatalf("memory-limited SELECT error = %v", err)
	}
	closeStatementResources(resources)
	executor.session.resources = nil

	result, err := executor.execute("SELECT name FROM authors LIMIT 1")
	if err != nil {
		t.Fatalf("session did not remain usable after memory exhaustion: %v", err)
	}
	if !reflect.DeepEqual(result.rows, [][]string{{"Ada"}}) {
		t.Fatalf("post-exhaustion rows = %#v", result.rows)
	}
}

func TestRelationalSelectSpillsOrderedRowsWithinTheTemporaryBudget(t *testing.T) {
	executor := relationalSelectExecutor(t)
	executor.streamRows = true
	config := Config{ResourceLimits: ResourceLimits{
		ExecutionMemoryLimitBytes:           50,
		AggregateExecutionMemoryLimitBytes:  50,
		TemporaryStorageLimitBytes:          1024,
		AggregateTemporaryStorageLimitBytes: 1024,
	}}
	manager := newResourceManager(config)
	resources := newStatementResources(manager, config, nil)
	executor.session.resources = resources
	defer func() {
		closeStatementResources(resources)
		executor.session.resources = nil
	}()
	result, err := executor.execute("SELECT name FROM authors ORDER BY name DESC")
	if err != nil {
		t.Fatalf("execute ordered SELECT: %v", err)
	}
	var rows [][]string
	if err := result.stream(func(row []string, _ []bool) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("stream spilled SELECT: %v", err)
	}
	if !reflect.DeepEqual(rows, [][]string{{"Linus"}, {"Grace"}, {"Ada"}}) {
		t.Fatalf("spilled ordered rows = %#v", rows)
	}
	if usage := manager.usage(); usage.SpillCount == 0 || usage.PeakTemporaryStorageBytes == 0 {
		t.Fatalf("spill usage = %#v", usage)
	}
}

func TestRelationalSpillSortCoalescesRunsWithBoundedFanIn(t *testing.T) {
	executor := relationalSelectExecutor(t)
	executor.streamRows = true
	for _, row := range [][]string{{"4", "same"}, {"5", "same"}, {"6", "same"}, {"7", "same"}, {"8", "same"}, {"9", "same"}, {"10", "same"}, {"11", "same"}, {"12", "same"}} {
		if err := executor.server.config.Catalog.Insert("app", "authors", row); err != nil {
			t.Fatalf("seed ordered row: %v", err)
		}
	}
	config := Config{ResourceLimits: ResourceLimits{
		ExecutionMemoryLimitBytes: 50, AggregateExecutionMemoryLimitBytes: 50,
		TemporaryStorageLimitBytes: 4096, AggregateTemporaryStorageLimitBytes: 4096,
	}}
	manager := newResourceManager(config)
	resources := newStatementResources(manager, config, nil)
	executor.session.resources = resources
	defer func() {
		closeStatementResources(resources)
		executor.session.resources = nil
	}()

	result, err := executor.execute("SELECT id FROM authors WHERE id >= 4 ORDER BY name, id")
	if err != nil {
		t.Fatalf("execute multi-run ordered SELECT: %v", err)
	}
	var rows [][]string
	if err := result.stream(func(row []string, _ []bool) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("stream multi-run ordered SELECT: %v", err)
	}
	want := [][]string{{"4"}, {"5"}, {"6"}, {"7"}, {"8"}, {"9"}, {"10"}, {"11"}, {"12"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("multi-run ordered rows = %#v, want %#v", rows, want)
	}
	if usage := manager.usage(); usage.SpillCount < 4 || usage.PeakExecutionMemoryBytes > 50 {
		t.Fatalf("multi-pass spill usage = %#v", usage)
	}
}

func TestRelationalSpillSortAppliesLimitWithoutAFlush(t *testing.T) {
	executor := relationalSelectExecutor(t)
	executor.streamRows = true
	resources := newStatementResources(executor.server.resources, executor.server.config, nil)
	executor.session.resources = resources
	defer func() {
		closeStatementResources(resources)
		executor.session.resources = nil
	}()
	result, err := executor.execute("SELECT name FROM authors ORDER BY name DESC LIMIT 2")
	if err != nil {
		t.Fatalf("execute ordered LIMIT SELECT: %v", err)
	}
	var rows [][]string
	if err := result.stream(func(row []string, _ []bool) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("stream ordered LIMIT SELECT: %v", err)
	}
	if !reflect.DeepEqual(rows, [][]string{{"Linus"}, {"Grace"}}) {
		t.Fatalf("ordered LIMIT rows = %#v", rows)
	}
}

func TestRelationalSelectTemporaryStorageExhaustionLeavesTheSessionUsable(t *testing.T) {
	executor := relationalSelectExecutor(t)
	executor.streamRows = true
	config := Config{ResourceLimits: ResourceLimits{
		ExecutionMemoryLimitBytes: 7, AggregateExecutionMemoryLimitBytes: 7,
		TemporaryStorageLimitBytes: 1, AggregateTemporaryStorageLimitBytes: 1,
	}}
	resources := newStatementResources(newResourceManager(config), config, nil)
	executor.session.resources = resources
	if _, err := executor.execute("SELECT name FROM authors ORDER BY name DESC"); !isFailureCode(err, 1114) {
		t.Fatalf("temporary-storage failure = %v", err)
	}
	if snapshot := statementResourceSnapshot(resources); snapshot.failure != "temporary_storage_exhausted" {
		t.Fatalf("resource failure evidence = %#v", snapshot)
	}
	closeStatementResources(resources)
	executor.session.resources = nil

	result, err := executor.execute("SELECT name FROM authors LIMIT 1")
	if err != nil {
		t.Fatalf("session did not remain usable after temporary exhaustion: %v", err)
	}
	result, err = materializeQueryResult(result)
	if err != nil {
		t.Fatalf("materialize post-exhaustion result: %v", err)
	}
	if !reflect.DeepEqual(result.rows, [][]string{{"Ada"}}) {
		t.Fatalf("post-exhaustion rows = %#v", result.rows)
	}
}

func TestRelationalSelectOuterJoinDistinctAndNulls(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("SELECT DISTINCT a.name, p.title FROM authors a LEFT JOIN posts p ON a.id = p.author_id ORDER BY a.name ASC, p.title ASC")
	if err != nil {
		t.Fatalf("left join: %v", err)
	}
	want := [][]string{{"Ada", "first"}, {"Ada", "second"}, {"Grace", "third"}, {"Linus", "NULL"}}
	if !reflect.DeepEqual(result.rows, want) || !reflect.DeepEqual(result.nulls, [][]bool{{false, false}, {false, false}, {false, false}, {false, true}}) {
		t.Fatalf("rows/nulls = %#v/%#v", result.rows, result.nulls)
	}
}

func TestRelationalSelectRejectsDistinctOrderOutsideProjection(t *testing.T) {
	executor := relationalSelectExecutor(t)
	if _, err := executor.execute("SELECT DISTINCT a.name FROM authors a JOIN posts p ON a.id = p.author_id ORDER BY p.id"); err == nil {
		t.Fatal("DISTINCT accepted ORDER BY outside the projection")
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

func TestRelationalSelectExplanationTracesProjectionExpression(t *testing.T) {
	executor := relationalSelectExecutor(t)
	result, err := executor.execute("EXPLAIN FORMAT=JSON SELECT p.score + 1 AS adjusted FROM posts p")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(result.rows[0][0]), &document); err != nil {
		t.Fatal(err)
	}
	project := document["plan"].(map[string]any)
	if project["kind"] != "project" {
		t.Fatalf("root = %#v", project)
	}
	columns := project["output"].(map[string]any)["columns"].([]any)
	if !slices.Contains(columns, any("p.score + 1")) {
		t.Fatalf("project output = %#v", columns)
	}
}
