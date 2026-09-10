package mysql

import "testing"

func TestIssue360CarriageReturnSeparatesTableAndWhere(t *testing.T) {
	executor := relationalSelectExecutor(t)
	for _, separator := range []string{" ", "\t", "\n", "\r", "\r\n"} {
		query := "SELECT id FROM authors" + separator + "WHERE id = 1"
		result, err := executeStatement(executor, query)
		if err != nil {
			t.Errorf("execute with separator %q: %v", separator, err)
			continue
		}
		if !equalRows(result.rows, [][]string{{"1"}}) {
			t.Errorf("execute with separator %q rows = %#v, want [[1]]", separator, result.rows)
		}
	}
}

func TestIssue360CarriageReturnDoesNotExposeKeywordsInQuotedText(t *testing.T) {
	if position := keywordAt("`quoted\rwhere name`", "where"); position >= 0 {
		t.Fatalf("keywordAt found WHERE inside quoted identifier at %d", position)
	}

	executor := relationalSelectExecutor(t)
	result, err := executeStatement(executor, "SELECT 'quoted\rWHERE text'")
	if err != nil {
		t.Fatalf("quoted literal: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"quoted\rWHERE text"}}) {
		t.Fatalf("quoted literal rows = %#v", result.rows)
	}

	result, err = executeStatement(executor, "SELECT id FROM authors /* quoted\rWHERE text */ WHERE id = 1")
	if err != nil {
		t.Fatalf("commented query: %v", err)
	}
	if !equalRows(result.rows, [][]string{{"1"}}) {
		t.Fatalf("commented query rows = %#v", result.rows)
	}
}
