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
