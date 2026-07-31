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

func TestMySQLAggregatesAndWindowsUseThePublicWireContract(t *testing.T) {
	runner := blackbox.Runner{Executable: executable}
	directory := initializedInstance(t, runner)
	process, address := startMySQLServer(t, runner, directory)
	defer func() { _ = process.Stop(); _ = process.Wait() }()
	client := newWireClient(t, address, "admin", "lifecycle-secret")
	defer client.close()

	for _, query := range []string{
		"CREATE DATABASE app",
		"USE app",
		"CREATE TABLE measurements (category VARCHAR(16), value INT)",
		"CREATE TABLE labels (value VARCHAR(8))",
		"CREATE TABLE required_values (value INT)",
		"CREATE TABLE approximate_values (value DOUBLE)",
		"CREATE TABLE large_values (value BIGINT)",
		"CREATE TABLE overflow_order (value BIGINT)",
		"CREATE TABLE ambiguous_groups (a INT, b INT)",
		"CREATE TABLE pairs (a INT, b INT)",
		"CREATE TABLE numbers (n INT)",
		"INSERT INTO measurements VALUES ('a', 5), ('a', 20), ('a', 20), ('b', 15), ('b', NULL)",
		"INSERT INTO labels VALUES ('a'), ('A')",
		"INSERT INTO approximate_values VALUES (1.5), (2.5)",
		"INSERT INTO large_values VALUES (9223372036854775807), (1)",
		"INSERT INTO overflow_order VALUES (9223372036854775807), (1)",
		"INSERT INTO ambiguous_groups VALUES (1, 7), (1, 8), (2, 7)",
		"INSERT INTO pairs VALUES (1, 1), (1, 1), (1, 2), (NULL, 1)",
		"INSERT INTO numbers VALUES (1), (2), (3)",
	} {
		if result := client.query(query); result.err != "" {
			t.Fatalf("%s: %#v", query, result)
		}
	}

	grouped := client.query("SELECT category, COUNT(*) AS rows, COUNT(value) AS present, COUNT(DISTINCT value) AS different, SUM(value) AS total, AVG(value) AS average, MIN(value) AS least, MAX(value) AS most FROM measurements GROUP BY category HAVING SUM(value) >= 15 ORDER BY category")
	wantGrouped := [][]string{{"a", "3", "3", "2", "45", "15.0000", "5", "20"}, {"b", "2", "1", "1", "15", "15.0000", "15", "15"}}
	if grouped.err != "" || !reflect.DeepEqual(grouped.rows, wantGrouped) {
		t.Fatalf("grouped aggregates: %#v", grouped)
	}
	groupedAlias := client.query("SELECT category AS c, SUM(value) AS total FROM measurements GROUP BY c ORDER BY c")
	if groupedAlias.err != "" || !reflect.DeepEqual(groupedAlias.rows, [][]string{{"a", "45"}, {"b", "15"}}) {
		t.Fatalf("GROUP BY projection alias: %#v", groupedAlias)
	}
	groupedOrdinal := client.query("SELECT category, SUM(value) AS total FROM measurements GROUP BY 1 ORDER BY 1")
	if groupedOrdinal.err != "" || !reflect.DeepEqual(groupedOrdinal.rows, [][]string{{"a", "45"}, {"b", "15"}}) {
		t.Fatalf("GROUP BY projection ordinal: %#v", groupedOrdinal)
	}
	ambiguousGroup := client.query("SELECT COUNT(*) AS a FROM ambiguous_groups GROUP BY a ORDER BY 1")
	if ambiguousGroup.err != "" || !reflect.DeepEqual(ambiguousGroup.rows, [][]string{{"1"}, {"2"}}) {
		t.Fatalf("GROUP BY source column before alias: %#v", ambiguousGroup)
	}
	if invalidDistinct := client.query("SELECT COUNT(DISTINCT *) FROM measurements"); !strings.HasPrefix(invalidDistinct.err, "42000") {
		t.Fatalf("COUNT(DISTINCT *): %#v", invalidDistinct)
	}
	if invalidHaving := client.query("SELECT category, COUNT(*) FROM measurements GROUP BY category HAVING value > 0"); !strings.HasPrefix(invalidHaving.err, "42000") {
		t.Fatalf("ungrouped HAVING column: %#v", invalidHaving)
	}
	if invalidOrder := client.query("SELECT category, COUNT(*) FROM measurements GROUP BY category ORDER BY value"); !strings.HasPrefix(invalidOrder.err, "42000") {
		t.Fatalf("ungrouped ORDER BY column: %#v", invalidOrder)
	}
	labels := client.query("SELECT value, COUNT(*), COUNT(DISTINCT value) FROM labels GROUP BY value")
	if labels.err != "" || !reflect.DeepEqual(labels.rows, [][]string{{"a", "2", "1"}}) {
		t.Fatalf("aggregate collation: %#v", labels)
	}
	pairCount := client.query("SELECT COUNT(DISTINCT a, b) FROM pairs")
	if pairCount.err != "" || !reflect.DeepEqual(pairCount.rows, [][]string{{"2"}}) {
		t.Fatalf("multi-column COUNT DISTINCT: %#v", pairCount)
	}
	emptyAverage := client.query("SELECT AVG(value) FROM required_values")
	if emptyAverage.err != "" || !reflect.DeepEqual(emptyAverage.rows, [][]string{{""}}) || len(emptyAverage.metadata) != 1 || emptyAverage.metadata[0].flags&0x0001 != 0 {
		t.Fatalf("empty AVG metadata: %#v", emptyAverage)
	}
	aggregateMetadata := client.query("SELECT COUNT(*), SUM(value), AVG(value) FROM measurements")
	if aggregateMetadata.err != "" || len(aggregateMetadata.metadata) != 3 || aggregateMetadata.metadata[0].typ != 0x08 || aggregateMetadata.metadata[0].length != 21 || aggregateMetadata.metadata[0].flags&0x0020 == 0 || aggregateMetadata.metadata[1].typ != 0xf6 || aggregateMetadata.metadata[1].length != 30 || aggregateMetadata.metadata[1].decimals != 0 || aggregateMetadata.metadata[1].flags&0x0001 != 0 || aggregateMetadata.metadata[2].typ != 0xf6 || aggregateMetadata.metadata[2].length != 16 || aggregateMetadata.metadata[2].decimals != 4 || aggregateMetadata.metadata[2].flags&0x0001 != 0 {
		t.Fatalf("aggregate result metadata: %#v", aggregateMetadata)
	}
	approximateMetadata := client.query("SELECT SUM(value), AVG(value) FROM approximate_values")
	if approximateMetadata.err != "" || len(approximateMetadata.metadata) != 2 || approximateMetadata.metadata[0].typ != 0x05 || approximateMetadata.metadata[0].decimals != 31 || approximateMetadata.metadata[1].typ != 0x05 || approximateMetadata.metadata[1].decimals != 31 {
		t.Fatalf("approximate aggregate metadata: %#v", approximateMetadata)
	}
	largeSum := client.query("SELECT SUM(value) FROM large_values")
	if largeSum.err != "" || !reflect.DeepEqual(largeSum.rows, [][]string{{"9223372036854775808"}}) || len(largeSum.metadata) != 1 || largeSum.metadata[0].typ != 0xf6 {
		t.Fatalf("large exact SUM: %#v", largeSum)
	}
	preparedText := client.query("SELECT category, COUNT(*) AS rows, SUM(value) AS total FROM measurements GROUP BY category ORDER BY category")
	prepared := client.prepare("SELECT category, COUNT(*) AS rows, SUM(value) AS total FROM measurements GROUP BY category ORDER BY category")
	if prepared.err != "" {
		t.Fatalf("prepare aggregate query: %#v", prepared)
	}
	defer client.closePrepared(prepared.id)
	preparedResult := client.executePrepared(prepared.id)
	if preparedResult.err != "" || !reflect.DeepEqual(preparedResult.rows, preparedText.rows) || !reflect.DeepEqual(preparedResult.metadata, preparedText.metadata) {
		t.Fatalf("prepared aggregate query: %#v", preparedResult)
	}
	mixed := client.query("SELECT category, SUM(value) AS total, RANK() OVER (ORDER BY SUM(value) DESC) AS total_rank FROM measurements GROUP BY category ORDER BY category")
	if mixed.err != "" || !reflect.DeepEqual(mixed.rows, [][]string{{"a", "45", "1"}, {"b", "15", "2"}}) {
		t.Fatalf("aggregate and window query: %#v", mixed)
	}
	groupedWindowAggregate := client.query("SELECT category, SUM(value) AS total, SUM(SUM(value)) OVER () AS all_total FROM measurements GROUP BY category ORDER BY category")
	if groupedWindowAggregate.err != "" || !reflect.DeepEqual(groupedWindowAggregate.rows, [][]string{{"a", "45", "60"}, {"b", "15", "60"}}) {
		t.Fatalf("grouped aggregate window: %#v", groupedWindowAggregate)
	}
	lowercaseGroupedWindowAggregate := client.query("SELECT category, SUM(value) AS total, SUM(sum(value)) OVER () AS all_total FROM measurements GROUP BY category ORDER BY category")
	if lowercaseGroupedWindowAggregate.err != "" || !reflect.DeepEqual(lowercaseGroupedWindowAggregate.rows, [][]string{{"a", "45", "60"}, {"b", "15", "60"}}) {
		t.Fatalf("lowercase grouped aggregate window: %#v", lowercaseGroupedWindowAggregate)
	}
	composedWindow := client.query("SELECT n + LAG(n, 1, 0) OVER (ORDER BY n) AS total FROM numbers ORDER BY n")
	if composedWindow.err != "" || !reflect.DeepEqual(composedWindow.rows, [][]string{{"1"}, {"3"}, {"5"}}) {
		t.Fatalf("composed window expression: %#v", composedWindow)
	}
	multipleComposedWindows := client.query("SELECT LAG(n, 1, 0) OVER ordered + LEAD(n, 1, 0) OVER ordered AS total FROM numbers WINDOW ordered AS (ORDER BY n) ORDER BY n")
	if multipleComposedWindows.err != "" || !reflect.DeepEqual(multipleComposedWindows.rows, [][]string{{"2"}, {"4"}, {"2"}}) {
		t.Fatalf("multiple composed window expressions: %#v", multipleComposedWindows)
	}
	composedAggregateWindow := client.query("SELECT n + SUM(n) OVER ordered AS total FROM numbers WINDOW ordered AS (ORDER BY n) ORDER BY n")
	if composedAggregateWindow.err != "" || !reflect.DeepEqual(composedAggregateWindow.rows, [][]string{{"2"}, {"5"}, {"9"}}) {
		t.Fatalf("composed aggregate window expression: %#v", composedAggregateWindow)
	}

	windowed := client.query("SELECT category, value, ROW_NUMBER() OVER ranked AS sequence, RANK() OVER ranked AS rank_value, DENSE_RANK() OVER ranked AS dense_rank_value, LAG(value, 1, 0) OVER ranked AS previous, LEAD(value, 1, 0) OVER ranked AS following, SUM(value) OVER ranked AS running_total FROM measurements WINDOW RANKED AS (PARTITION BY category ORDER BY value DESC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) ORDER BY category, sequence")
	wantWindowed := [][]string{{"a", "20", "1", "1", "1", "0", "20", "45"}, {"a", "20", "2", "1", "1", "20", "5", "45"}, {"a", "5", "3", "3", "2", "20", "0", "45"}, {"b", "15", "1", "1", "1", "0", "", "15"}, {"b", "", "2", "2", "2", "15", "0", "15"}}
	if windowed.err != "" || !reflect.DeepEqual(windowed.rows, wantWindowed) {
		t.Fatalf("window functions: %#v", windowed)
	}
	for _, index := range []int{2, 3, 4} {
		if len(windowed.metadata) <= index || windowed.metadata[index].typ != 0x08 || windowed.metadata[index].length != 21 || windowed.metadata[index].flags&0x0020 == 0 {
			t.Fatalf("window integer metadata: %#v", windowed)
		}
	}
	textDefault := client.query("SELECT LAG(value, 1, 'missing') OVER (ORDER BY value) FROM measurements ORDER BY value")
	if textDefault.err != "" || !reflect.DeepEqual(textDefault.rows, [][]string{{"missing"}, {""}, {"5"}, {"15"}, {"20"}}) || len(textDefault.metadata) != 1 || textDefault.metadata[0].typ != 0xfd {
		t.Fatalf("window text default: %#v", textDefault)
	}
	decimalDefault := client.query("SELECT LAG(value, 1, 0.5) OVER (ORDER BY value) FROM measurements")
	if decimalDefault.err != "" || len(decimalDefault.metadata) != 1 || decimalDefault.metadata[0].typ != 0xf6 {
		t.Fatalf("window decimal default: %#v", decimalDefault)
	}
	doubleDefault := client.query("SELECT LAG(value, 1, 0e0) OVER (ORDER BY value) FROM measurements")
	if doubleDefault.err != "" || len(doubleDefault.metadata) != 1 || doubleDefault.metadata[0].typ != 0x05 || doubleDefault.metadata[0].decimals != 31 {
		t.Fatalf("window double default: %#v", doubleDefault)
	}

	framed := client.query("SELECT value, SUM(value) OVER (ORDER BY value ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) AS rows_frame, SUM(value) OVER (ORDER BY value RANGE BETWEEN CURRENT ROW AND CURRENT ROW) AS range_frame FROM measurements ORDER BY value")
	wantFramed := [][]string{{"", "", ""}, {"5", "5", "5"}, {"15", "20", "15"}, {"20", "35", "40"}, {"20", "40", "40"}}
	if framed.err != "" || !reflect.DeepEqual(framed.rows, wantFramed) {
		t.Fatalf("window frames: %#v", framed)
	}
	ranged := client.query("SELECT value, SUM(value) OVER (ORDER BY value RANGE BETWEEN 10 PRECEDING AND CURRENT ROW) FROM measurements WHERE value IS NOT NULL ORDER BY value")
	if ranged.err != "" || !reflect.DeepEqual(ranged.rows, [][]string{{"5", "5"}, {"15", "20"}, {"20", "55"}, {"20", "55"}}) {
		t.Fatalf("numeric RANGE frame: %#v", ranged)
	}
	rangedNull := client.query("SELECT value, SUM(value) OVER (ORDER BY value RANGE BETWEEN 10 PRECEDING AND CURRENT ROW) FROM measurements ORDER BY value")
	if rangedNull.err != "" || !reflect.DeepEqual(rangedNull.rows, [][]string{{"", ""}, {"5", "5"}, {"15", "20"}, {"20", "55"}, {"20", "55"}}) {
		t.Fatalf("numeric RANGE frame with NULL order value: %#v", rangedNull)
	}
	descendingRange := client.query("SELECT value, SUM(value) OVER (ORDER BY value DESC RANGE BETWEEN 10 PRECEDING AND CURRENT ROW) FROM measurements WHERE value IS NOT NULL ORDER BY value DESC")
	if descendingRange.err != "" || !reflect.DeepEqual(descendingRange.rows, [][]string{{"20", "40"}, {"20", "40"}, {"15", "55"}, {"5", "20"}}) {
		t.Fatalf("descending numeric RANGE frame: %#v", descendingRange)
	}
	partitioned := client.query("SELECT value, ROW_NUMBER() OVER (PARTITION BY value ORDER BY value) FROM labels ORDER BY value")
	if partitioned.err != "" || !reflect.DeepEqual(partitioned.rows, [][]string{{"a", "1"}, {"A", "2"}}) {
		t.Fatalf("window partition collation: %#v", partitioned)
	}
	largeOffset := client.query("SELECT LAG(value, 9223372036854775808, 0) OVER (ORDER BY value) FROM measurements ORDER BY value")
	if largeOffset.err != "" || !reflect.DeepEqual(largeOffset.rows, [][]string{{"0"}, {"0"}, {"0"}, {"0"}, {"0"}}) {
		t.Fatalf("large window offset: %#v", largeOffset)
	}
	for _, invalid := range []struct {
		query string
		code  uint16
		state string
	}{
		{"SELECT SUM(value) OVER (ORDER BY value ROWS BETWEEN CURRENT ROW AND 1 PRECEDING) FROM measurements", 1064, "42000"},
		{"SELECT SUM(value) OVER (RANGE 1 PRECEDING) FROM measurements", 3587, "HY000"},
		{"SELECT SUM(DISTINCT value) OVER () FROM measurements", 1235, "42000"},
		{"SELECT value + SUM(DISTINCT value) OVER () FROM measurements", 1235, "42000"},
		{"SELECT ROW_NUMBER() OVER (ORDER BY value + 1) FROM overflow_order", 1690, "22003"},
		{"SELECT FIRST_VALUE(value, 9) OVER () FROM measurements", 1064, "42000"},
		{"SELECT LAST_VALUE(value, 9) OVER () FROM measurements", 1064, "42000"},
		{"SELECT NTH_VALUE(value, 0) OVER () FROM measurements", 1210, "HY000"},
		{"SELECT LAG(value, 1 + 1) OVER () FROM measurements", 1210, "HY000"},
		{"SELECT LEAD(value, 1 + 1) OVER () FROM measurements", 1210, "HY000"},
		{"SELECT NTH_VALUE(value, 1 + 1) OVER () FROM measurements", 1210, "HY000"},
	} {
		if result := client.query(invalid.query); result.errCode != invalid.code || result.errState != invalid.state {
			t.Fatalf("invalid window query %q: %#v", invalid.query, result)
		}
	}

	explained := client.query("EXPLAIN FORMAT=JSON SELECT category, SUM(value) AS total, ROW_NUMBER() OVER ranked + RANK() OVER ranked AS sequence FROM measurements GROUP BY category WINDOW ranked AS (ORDER BY SUM(value))")
	if explained.err != "" || len(explained.rows) != 1 {
		t.Fatalf("aggregate/window explanation: %#v", explained)
	}
	var document struct {
		Plan struct {
			Kind     string `json:"kind"`
			Children []struct {
				Kind      string `json:"kind"`
				Operation struct {
					WindowCount   int `json:"window_count"`
					FunctionCount int `json:"function_count"`
				} `json:"operation"`
				Children []struct {
					Kind      string `json:"kind"`
					Operation struct {
						Purpose string `json:"purpose"`
					} `json:"operation"`
					Children []struct {
						Kind      string `json:"kind"`
						Operation struct {
							GroupingExpressions []string `json:"grouping_expressions"`
						} `json:"operation"`
					} `json:"children"`
				} `json:"children"`
			} `json:"children"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(explained.rows[0][0]), &document); err != nil {
		t.Fatalf("decode aggregate/window explanation: %v", err)
	}
	if document.Plan.Kind != "project" || len(document.Plan.Children) != 1 || document.Plan.Children[0].Kind != "window" || document.Plan.Children[0].Operation.WindowCount != 1 || document.Plan.Children[0].Operation.FunctionCount != 2 || len(document.Plan.Children[0].Children) != 1 || document.Plan.Children[0].Children[0].Kind != "sort" || document.Plan.Children[0].Children[0].Operation.Purpose != "window" || len(document.Plan.Children[0].Children[0].Children) != 1 || document.Plan.Children[0].Children[0].Children[0].Kind != "aggregate" || !reflect.DeepEqual(document.Plan.Children[0].Children[0].Children[0].Operation.GroupingExpressions, []string{"category"}) {
		t.Fatalf("aggregate/window explanation chain: %#v", document)
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
	multiTermCollation := client.query("SELECT 'A' UNION SELECT name FROM binary_names UNION SELECT 'B' ORDER BY 1")
	if multiTermCollation.err != "" || !reflect.DeepEqual(multiTermCollation.rows, [][]string{{"A"}, {"B"}, {"a"}}) {
		t.Fatalf("multi-term set coercibility: %#v", multiTermCollation)
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
	if cteReuseExplanation.err != "" || len(cteReuseExplanation.rows) != 1 || strings.Count(cteReuseExplanation.rows[0][0], `"reason":"cte"`) != 1 || !strings.Contains(cteReuseExplanation.rows[0][0], `"reason":"reuse"`) || strings.Index(cteReuseExplanation.rows[0][0], `"reason":"cte"`) > strings.Index(cteReuseExplanation.rows[0][0], `"reason":"reuse"`) {
		t.Fatalf("CTE reuse explanation: %#v", cteReuseExplanation)
	}
	cteOrderExplanation := client.query("EXPLAIN FORMAT=JSON WITH first_ids AS (SELECT id FROM authors), second_ids AS (SELECT author_id FROM posts) SELECT a.id FROM first_ids a JOIN second_ids b ON a.id = b.author_id")
	if cteOrderExplanation.err != "" || len(cteOrderExplanation.rows) != 1 || strings.Index(cteOrderExplanation.rows[0][0], `"fragment":"a"`) > strings.Index(cteOrderExplanation.rows[0][0], `"fragment":"b"`) {
		t.Fatalf("CTE explanation input order: %#v", cteOrderExplanation)
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
