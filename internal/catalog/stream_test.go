package catalog_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestWriteStreamsACompleteCatalog(t *testing.T) {
	definition := catalog.Definition{
		Namespaces: map[string]catalog.Namespace{
			"app": {Name: "app", Tables: map[string]catalog.Table{
				"items": {Name: "items", Columns: []string{"id", "value"}, Rows: [][]string{{"1", "a"}, {"2", "b"}}},
			}},
		},
		Accounts: map[string]catalog.Account{"admin": {Name: "admin", PasswordHash: "hash"}},
	}
	var encoded bytes.Buffer
	if err := catalog.Write(&encoded, definition); err != nil {
		t.Fatal(err)
	}
	var decoded catalog.Definition
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, definition) {
		t.Fatalf("decoded catalog = %#v, want %#v", decoded, definition)
	}
}
