package blackbox_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

type conformanceEvidenceInventory struct {
	Schema  string `json:"schema"`
	Stories []struct {
		ID       int      `json:"id"`
		Evidence []string `json:"evidence"`
	} `json:"stories"`
}

func TestConformanceEvidenceMapCoversEveryIssueStory(t *testing.T) {
	root := repositoryRoot()
	inventoryPath := filepath.Join(root, "docs", "conformance-evidence.json")
	documentPath := filepath.Join(root, "docs", "conformance-evidence.md")
	if _, err := os.Stat(documentPath); err != nil {
		t.Fatalf("conformance evidence document missing: %v", err)
	}

	raw, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatalf("read conformance inventory: %v", err)
	}
	var inventory conformanceEvidenceInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatalf("decode conformance inventory: %v", err)
	}
	if inventory.Schema != "database.conformance-evidence/v1" {
		t.Fatalf("inventory schema = %q", inventory.Schema)
	}

	tests := blackboxTestNames(t, filepath.Join(root, "test", "blackbox"))
	seen := map[int]bool{}
	for _, story := range inventory.Stories {
		if story.ID < 1 || story.ID > 70 {
			t.Fatalf("story id %d outside 1..70", story.ID)
		}
		if seen[story.ID] {
			t.Fatalf("duplicate story id %d", story.ID)
		}
		seen[story.ID] = true
		if len(story.Evidence) == 0 {
			t.Fatalf("story %d has no evidence", story.ID)
		}
		for _, item := range story.Evidence {
			assertEvidenceExists(t, root, tests, item)
		}
	}
	for id := 1; id <= 70; id++ {
		if !seen[id] {
			t.Fatalf("story %d is unmapped", id)
		}
	}
}

func assertEvidenceExists(t *testing.T, root string, tests map[string]bool, item string) {
	t.Helper()
	if tests[item] {
		return
	}
	path := filepath.Join(root, item)
	if _, err := os.Stat(path); err == nil {
		return
	}
	t.Fatalf("evidence %q is not a black-box test or repository path", item)
}

func blackboxTestNames(t *testing.T, directory string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read blackbox tests: %v", err)
	}
	fileSet := token.NewFileSet()
	names := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name == nil {
				continue
			}
			name := function.Name.Name
			if len(name) >= 4 && name[:4] == "Test" {
				names[name] = true
			}
		}
	}
	return names
}
