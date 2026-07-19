package blackbox_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jonbaldie/database/test/blackbox"
)

func TestMySQLRelationalShapingMatchesTextAndPreparedWirePaths(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE authors (id INT, name VARCHAR(32))",
		"CREATE TABLE posts (id INT, author_id INT, title VARCHAR(32), score INT)",
		"CREATE TABLE labels (id INT, label VARCHAR(32))",
		"INSERT INTO authors VALUES (1, 'Ada'), (2, 'Grace'), (3, 'Linus')",
		"INSERT INTO posts VALUES (10, 1, 'first', 5), (11, 1, 'second', 20), (12, 2, 'third', 15)",
		"INSERT INTO labels VALUES (1, 'A'), (2, 'G')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	text := client.query("SELECT a.name, p.title FROM authors a JOIN posts p ON a.id = p.author_id WHERE p.score >= 10 ORDER BY p.id DESC LIMIT 2")
	if text.err != "" {
		t.Fatalf("text select: %#v", text)
	}
	prepared := client.prepare("SELECT a.name, p.title FROM authors a JOIN posts p ON a.id = p.author_id WHERE p.score >= ? ORDER BY p.id DESC LIMIT ?")
	if prepared.err != "" {
		t.Fatalf("prepare select: %#v", prepared)
	}
	defer client.closePrepared(prepared.id)
	preparedResult := client.executePreparedValues(prepared.id, []preparedParameter{
		{typ: 0x03, value: []byte{10, 0, 0, 0}},
		{typ: 0x03, value: []byte{2, 0, 0, 0}},
	})
	if preparedResult.err != "" || !reflect.DeepEqual(preparedResult.rows, text.rows) || !reflect.DeepEqual(preparedResult.metadata, text.metadata) {
		t.Fatalf("prepared select differs: text=%#v prepared=%#v", text, preparedResult)
	}

	using := client.query("SELECT * FROM authors a LEFT JOIN labels l USING (id) ORDER BY id")
	wantUsing := [][]string{{"1", "Ada", "A"}, {"2", "Grace", "G"}, {"3", "Linus", ""}}
	if using.err != "" || !reflect.DeepEqual(using.rows, wantUsing) || strings.Join(using.columns, ",") != "id,name,label" {
		t.Fatalf("using select: %#v", using)
	}

	distinct := client.query("SELECT DISTINCT a.name FROM authors a CROSS JOIN labels l ORDER BY a.name DESC LIMIT 2")
	if distinct.err != "" || !reflect.DeepEqual(distinct.rows, [][]string{{"Linus"}, {"Grace"}}) {
		t.Fatalf("distinct/cross select: %#v", distinct)
	}
	computed := client.query("SELECT p.score + 1 AS adjusted FROM posts p WHERE p.score * 2 >= 30 ORDER BY adjusted DESC")
	if computed.err != "" || !reflect.DeepEqual(computed.rows, [][]string{{"21"}, {"16"}}) {
		t.Fatalf("computed select: %#v", computed)
	}

	explained := client.query("EXPLAIN FORMAT=JSON SELECT DISTINCT a.name FROM authors a JOIN posts p ON a.id = p.author_id ORDER BY a.name DESC LIMIT 1")
	if explained.err != "" || len(explained.rows) != 1 {
		t.Fatalf("explain select: %#v", explained)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(explained.rows[0][0]), &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["plan"]; !ok {
		t.Fatalf("explanation missing plan: %#v", document)
	}
}

func TestMySQLComposedQueriesMatchTextAndPreparedWirePaths(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE authors (id INT, name VARCHAR(32))",
		"CREATE TABLE posts (id INT, author_id INT, title VARCHAR(32), score INT)",
		"CREATE TABLE binary_names (name VARCHAR(32) COLLATE utf8mb4_bin)",
		"CREATE TABLE temporal_values (d DATE, dt DATETIME)",
		"INSERT INTO authors VALUES (1, 'Ada'), (2, 'Grace'), (3, 'Linus')",
		"INSERT INTO posts VALUES (10, 1, 'first', 5), (11, 1, 'second', 20), (12, 2, 'third', 15)",
		"INSERT INTO binary_names VALUES ('a'), ('B')",
		"INSERT INTO temporal_values VALUES ('2024-01-01', '2024-01-02 03:04:05')",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	derived := client.query("SELECT d.name FROM (SELECT name, id FROM authors WHERE id >= 2) AS d ORDER BY d.id DESC")
	if derived.err != "" || !reflect.DeepEqual(derived.rows, [][]string{{"Linus"}, {"Grace"}}) {
		t.Fatalf("derived table: %#v", derived)
	}

	cte := client.query("WITH recent AS (SELECT author_id, title FROM posts WHERE score >= 15) SELECT a.name, recent.title FROM authors a JOIN recent ON a.id = recent.author_id ORDER BY recent.title")
	if cte.err != "" || !reflect.DeepEqual(cte.rows, [][]string{{"Ada", "second"}, {"Grace", "third"}}) {
		t.Fatalf("CTE: %#v", cte)
	}
	unusedCTE := client.query("WITH unused AS (SELECT 1 / 0 AS broken) SELECT 1")
	if unusedCTE.err != "" || !reflect.DeepEqual(unusedCTE.rows, [][]string{{"1"}}) {
		t.Fatalf("unused CTE executed: %#v", unusedCTE)
	}
	shadowedCTE := client.query("WITH x AS (SELECT id AS n FROM authors WHERE id = 1) SELECT n FROM (WITH x AS (SELECT id AS n FROM authors WHERE id = 2) SELECT n FROM x) AS nested")
	if shadowedCTE.err != "" || !reflect.DeepEqual(shadowedCTE.rows, [][]string{{"2"}}) {
		t.Fatalf("inner CTE shadowing: %#v", shadowedCTE)
	}

	correlated := client.query("SELECT a.name, (SELECT p.title FROM posts p WHERE p.author_id = a.id ORDER BY p.id LIMIT 1) AS first_title FROM authors a ORDER BY a.id")
	wantCorrelated := [][]string{{"Ada", "first"}, {"Grace", "third"}, {"Linus", ""}}
	if correlated.err != "" || !reflect.DeepEqual(correlated.rows, wantCorrelated) {
		t.Fatalf("correlated scalar: %#v", correlated)
	}
	outerProjection := client.query("SELECT a.id, (SELECT a.id + p.id FROM posts p WHERE p.author_id = a.id ORDER BY p.id LIMIT 1) AS combined FROM authors a WHERE a.id <= 2 ORDER BY a.id")
	if outerProjection.err != "" || !reflect.DeepEqual(outerProjection.rows, [][]string{{"1", "11"}, {"2", "14"}}) {
		t.Fatalf("outer projection scope: %#v", outerProjection)
	}
	nestedOuterProjection := client.query("SELECT (SELECT (SELECT p.author_id) FROM posts p WHERE p.author_id = a.id ORDER BY p.id LIMIT 1) FROM authors a WHERE a.id = 1")
	if nestedOuterProjection.err != "" || !reflect.DeepEqual(nestedOuterProjection.rows, [][]string{{"1"}}) {
		t.Fatalf("nested outer projection scope: %#v", nestedOuterProjection)
	}
	existsProjection := client.query("SELECT a.name FROM authors a WHERE EXISTS (SELECT 1 / (p.id - p.id) FROM posts p WHERE p.author_id = a.id) ORDER BY a.id")
	if existsProjection.err != "" || !reflect.DeepEqual(existsProjection.rows, [][]string{{"Ada"}, {"Grace"}}) {
		t.Fatalf("EXISTS evaluated projection: %#v", existsProjection)
	}

	predicates := client.query("SELECT a.name FROM authors a WHERE EXISTS (SELECT p.id FROM posts p WHERE p.author_id = a.id) AND a.id IN (SELECT p.author_id FROM posts p WHERE p.score >= 15) ORDER BY a.id")
	if predicates.err != "" || !reflect.DeepEqual(predicates.rows, [][]string{{"Ada"}, {"Grace"}}) {
		t.Fatalf("subquery predicates: %#v", predicates)
	}

	prepared := client.prepare("SELECT a.name FROM authors a WHERE a.id IN (SELECT p.author_id FROM posts p WHERE p.score >= ?) ORDER BY a.id")
	if prepared.err != "" {
		t.Fatalf("prepare composed query: %#v", prepared)
	}
	defer client.closePrepared(prepared.id)
	preparedResult := client.executePreparedValues(prepared.id, []preparedParameter{{typ: 0x03, value: []byte{15, 0, 0, 0}}})
	if preparedResult.err != "" || !reflect.DeepEqual(preparedResult.rows, [][]string{{"Ada"}, {"Grace"}}) || !reflect.DeepEqual(preparedResult.metadata, predicates.metadata) {
		t.Fatalf("prepared composed query: %#v", preparedResult)
	}

	set := client.query("SELECT author_id FROM posts UNION ALL SELECT id FROM authors UNION SELECT author_id FROM posts ORDER BY author_id DESC LIMIT 4")
	if set.err != "" || !reflect.DeepEqual(set.rows, [][]string{{"3"}, {"2"}, {"1"}}) {
		t.Fatalf("set operation: %#v", set)
	}
	if strings.Join(set.columns, ",") != "author_id" || len(set.metadata) != 1 || set.metadata[0].table != "posts" {
		t.Fatalf("set metadata did not come from first term: %#v", set)
	}
	intersect := client.query("SELECT id FROM authors INTERSECT ALL SELECT author_id FROM posts ORDER BY id")
	if intersect.err != "" || !reflect.DeepEqual(intersect.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("INTERSECT ALL: %#v", intersect)
	}
	except := client.query("SELECT id FROM authors EXCEPT SELECT author_id FROM posts ORDER BY id")
	if except.err != "" || !reflect.DeepEqual(except.rows, [][]string{{"3"}}) {
		t.Fatalf("EXCEPT: %#v", except)
	}
	unionAll := client.query("SELECT author_id FROM posts UNION ALL SELECT id FROM authors ORDER BY author_id")
	wantUnionAll := [][]string{{"1"}, {"1"}, {"1"}, {"2"}, {"2"}, {"3"}}
	if unionAll.err != "" || !reflect.DeepEqual(unionAll.rows, wantUnionAll) {
		t.Fatalf("UNION ALL: %#v", unionAll)
	}
	exceptAll := client.query("SELECT author_id FROM posts EXCEPT ALL SELECT id FROM authors ORDER BY author_id")
	if exceptAll.err != "" || !reflect.DeepEqual(exceptAll.rows, [][]string{{"1"}}) {
		t.Fatalf("EXCEPT ALL: %#v", exceptAll)
	}
	precedence := client.query("SELECT id FROM authors WHERE id = 1 UNION SELECT id FROM authors WHERE id = 2 INTERSECT SELECT author_id FROM posts WHERE author_id = 2 ORDER BY id")
	if precedence.err != "" || !reflect.DeepEqual(precedence.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("set precedence: %#v", precedence)
	}
	grouped := client.query("(SELECT id FROM authors WHERE id = 1 UNION SELECT id FROM authors WHERE id = 2) INTERSECT SELECT author_id FROM posts ORDER BY id")
	if grouped.err != "" || !reflect.DeepEqual(grouped.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("grouped set expression: %#v", grouped)
	}
	collated := client.query("SELECT name FROM authors WHERE id = 1 UNION SELECT 'ada'")
	if collated.err != "" || !reflect.DeepEqual(collated.rows, [][]string{{"Ada"}}) {
		t.Fatalf("collated set duplicate: %#v", collated)
	}
	promoted := client.query("SELECT id FROM authors WHERE id = 1 UNION ALL SELECT 2.50")
	if promoted.err != "" || len(promoted.metadata) != 1 || promoted.metadata[0].typ != 0xf6 || !reflect.DeepEqual(promoted.rows, [][]string{{"1.00"}, {"2.50"}}) {
		t.Fatalf("set numeric promotion: %#v", promoted)
	}
	strictSet := client.query("SELECT CAST(1 AS UNSIGNED) UNION SELECT 1")
	if !strings.HasPrefix(strictSet.err, "22007") {
		t.Fatalf("set accepted signed/unsigned conversion: %#v", strictSet)
	}
	binaryOrder := client.query("SELECT name FROM binary_names UNION ALL SELECT name FROM binary_names ORDER BY name LIMIT 2")
	if binaryOrder.err != "" || !reflect.DeepEqual(binaryOrder.rows, [][]string{{"B"}, {"B"}}) {
		t.Fatalf("set binary collation order: %#v", binaryOrder)
	}
	mixedCollation := client.query("SELECT name FROM binary_names WHERE name = 'a' UNION SELECT 'A' ORDER BY name")
	if mixedCollation.err != "" || !reflect.DeepEqual(mixedCollation.rows, [][]string{{"A"}, {"a"}}) {
		t.Fatalf("set collation coercibility: %#v", mixedCollation)
	}
	ambiguousCollation := client.query("SELECT name FROM authors UNION SELECT name FROM binary_names")
	if !strings.HasPrefix(ambiguousCollation.err, "22007") {
		t.Fatalf("set accepted ambiguous collation: %#v", ambiguousCollation)
	}
	computedCollation := client.query("SELECT UPPER(name) FROM binary_names UNION SELECT LOWER(name) FROM binary_names ORDER BY 1")
	if computedCollation.err != "" || !reflect.DeepEqual(computedCollation.rows, [][]string{{"A"}, {"B"}, {"a"}, {"b"}}) {
		t.Fatalf("computed set collation: %#v", computedCollation)
	}
	temporalSet := client.query("SELECT d FROM temporal_values UNION ALL SELECT dt FROM temporal_values")
	if temporalSet.err != "" || !reflect.DeepEqual(temporalSet.rows, [][]string{{"2024-01-01 00:00:00"}, {"2024-01-02 03:04:05"}}) {
		t.Fatalf("set temporal promotion: %#v", temporalSet)
	}
	derivedBinary := client.query("SELECT name FROM (SELECT name FROM binary_names) AS d WHERE name = 'A'")
	if derivedBinary.err != "" || len(derivedBinary.rows) != 0 {
		t.Fatalf("derived collation metadata: %#v", derivedBinary)
	}
	nullOrder := client.query("SELECT name FROM authors UNION ALL SELECT NULL FROM authors ORDER BY name LIMIT 2")
	if nullOrder.err != "" || !reflect.DeepEqual(nullOrder.rows, [][]string{{""}, {""}}) {
		t.Fatalf("set NULL ordering: %#v", nullOrder)
	}

	cardinality := client.query("SELECT (SELECT p.title FROM posts p WHERE p.author_id = 1) FROM authors LIMIT 1")
	if !strings.HasPrefix(cardinality.err, "21000") || !strings.Contains(cardinality.err, "more than 1 row") {
		t.Fatalf("scalar cardinality: %#v", cardinality)
	}

	explained := client.query("EXPLAIN FORMAT=JSON WITH recent AS (SELECT author_id FROM posts) SELECT a.name FROM authors a WHERE a.id IN (SELECT recent.author_id FROM recent) UNION SELECT name FROM authors")
	if explained.err != "" || len(explained.rows) != 1 {
		t.Fatalf("composed explanation: %#v", explained)
	}
	for _, evidence := range []string{`"kind":"set_operation"`, `"set_operation":"union"`, `"reason":"cte"`, `"reason":"subquery"`, `"clause":"where"`} {
		if !strings.Contains(explained.rows[0][0], evidence) {
			t.Errorf("explanation missing %s: %s", evidence, explained.rows[0][0])
		}
	}
	correlatedExplanation := client.query("EXPLAIN FORMAT=JSON SELECT a.name, (SELECT p.title FROM posts p WHERE p.author_id = a.id ORDER BY p.id LIMIT 1) AS first_title FROM authors a")
	if correlatedExplanation.err != "" || len(correlatedExplanation.rows) != 1 || !strings.Contains(correlatedExplanation.rows[0][0], "Evaluate the correlated subquery") {
		t.Fatalf("correlated explanation: %#v", correlatedExplanation)
	}
	outerScalarExplanation := client.query("EXPLAIN FORMAT=JSON SELECT a.id, (SELECT a.id + 1) FROM authors a")
	if outerScalarExplanation.err != "" || len(outerScalarExplanation.rows) != 1 || !strings.Contains(outerScalarExplanation.rows[0][0], "Evaluate the correlated subquery") {
		t.Fatalf("outer scalar explanation: %#v", outerScalarExplanation)
	}
	existsSetExplanation := client.query("EXPLAIN FORMAT=JSON SELECT name FROM authors WHERE EXISTS (SELECT 1 UNION SELECT 2)")
	if existsSetExplanation.err != "" || len(existsSetExplanation.rows) != 1 || !strings.Contains(existsSetExplanation.rows[0][0], `"kind":"set_operation"`) {
		t.Fatalf("EXISTS set explanation: %#v", existsSetExplanation)
	}
	existsSetProjection := client.query("SELECT name FROM authors WHERE EXISTS (SELECT 1 / (id - id) FROM authors UNION SELECT 2) ORDER BY id LIMIT 1")
	if existsSetProjection.err != "" || !reflect.DeepEqual(existsSetProjection.rows, [][]string{{"Ada"}}) {
		t.Fatalf("EXISTS set evaluated projection: %#v", existsSetProjection)
	}
	explainExistsSetProjection := client.query("EXPLAIN FORMAT=JSON SELECT name FROM authors WHERE EXISTS (SELECT 1 / (id - id) FROM authors UNION SELECT 2)")
	if explainExistsSetProjection.err != "" || len(explainExistsSetProjection.rows) != 1 || !strings.Contains(explainExistsSetProjection.rows[0][0], `"kind":"set_operation"`) {
		t.Fatalf("EXISTS set plan-only explanation: %#v", explainExistsSetProjection)
	}
	missingSubqueryColumn := client.query("SELECT id FROM authors WHERE id < 0 AND EXISTS (SELECT missing FROM posts)")
	if !strings.HasPrefix(missingSubqueryColumn.err, "42S22") {
		t.Fatalf("missing subquery column deferred: %#v", missingSubqueryColumn)
	}
	cteReuseExplanation := client.query("EXPLAIN FORMAT=JSON WITH ids AS (SELECT id FROM authors) SELECT a.id FROM ids a JOIN ids b ON a.id = b.id")
	if cteReuseExplanation.err != "" || len(cteReuseExplanation.rows) != 1 || strings.Count(cteReuseExplanation.rows[0][0], `"reason":"cte"`) != 1 || !strings.Contains(cteReuseExplanation.rows[0][0], `"reason":"reuse"`) {
		t.Fatalf("CTE reuse explanation: %#v", cteReuseExplanation)
	}
	preparedCardinality := client.prepare("SELECT (SELECT name FROM authors)")
	if preparedCardinality.err != "" {
		t.Fatalf("prepare executed scalar subquery: %#v", preparedCardinality)
	}
	defer client.closePrepared(preparedCardinality.id)
	if executed := client.executePrepared(preparedCardinality.id); !strings.HasPrefix(executed.err, "21000") {
		t.Fatalf("prepared scalar cardinality execution: %#v", executed)
	}
	for _, query := range []string{
		"EXPLAIN FORMAT=JSON WITH broken AS (SELECT 1 / (id - 2) AS value FROM authors) SELECT value FROM broken",
		"EXPLAIN FORMAT=JSON SELECT (SELECT name FROM authors LIMIT 1)",
		"EXPLAIN FORMAT=JSON SELECT a.name FROM authors a JOIN posts p ON EXISTS (SELECT id FROM posts WHERE author_id = a.id)",
		"EXPLAIN FORMAT=JSON SELECT (SELECT name FROM authors)",
	} {
		result := client.query(query)
		if result.err != "" || len(result.rows) != 1 || (!strings.Contains(result.rows[0][0], `"reason":"subquery"`) && !strings.Contains(result.rows[0][0], `"reason":"cte"`) && !strings.Contains(result.rows[0][0], "Evaluate the correlated subquery")) {
			t.Fatalf("plan-only composed explanation %q: %#v", query, result)
		}
	}
}
