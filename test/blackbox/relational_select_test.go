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
		"INSERT INTO authors VALUES (1, 'Ada'), (2, 'Grace'), (3, 'Linus')",
		"INSERT INTO posts VALUES (10, 1, 'first', 5), (11, 1, 'second', 20), (12, 2, 'third', 15)",
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

	correlated := client.query("SELECT a.name, (SELECT p.title FROM posts p WHERE p.author_id = a.id ORDER BY p.id LIMIT 1) AS first_title FROM authors a ORDER BY a.id")
	wantCorrelated := [][]string{{"Ada", "first"}, {"Grace", "third"}, {"Linus", ""}}
	if correlated.err != "" || !reflect.DeepEqual(correlated.rows, wantCorrelated) {
		t.Fatalf("correlated scalar: %#v", correlated)
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
	if correlatedExplanation.err != "" || len(correlatedExplanation.rows) != 1 || !strings.Contains(correlatedExplanation.rows[0][0], `"reason":"subquery"`) {
		t.Fatalf("correlated explanation: %#v", correlatedExplanation)
	}
}
