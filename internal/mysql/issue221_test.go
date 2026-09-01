package mysql

import "testing"

func TestIssue221DoubleRendersDecimalNotation(t *testing.T) {
	tests := map[string]string{
		"1e9":    "1000000000",
		"1e10":   "10000000000",
		"1e20":   "100000000000000000000",
		"1e-4":   "0.0001",
		"1e-10":  "0.0000000001",
		"-1e10":  "-10000000000",
		"-1e-10": "-0.0000000001",
		"1.25":   "1.25",
	}

	for expression, want := range tests {
		if got := evalRender(t, expression); got != want {
			t.Errorf("evaluateScalar(%q) = %q, want %q", expression, got, want)
		}
	}
}

func TestIssue221TextSelectUsesDecimalNotation(t *testing.T) {
	executor := ddlExecutorForTest(t)
	result, err := executeStatement(executor, "SELECT 1e10, 1e-10, -1e20")
	if err != nil {
		t.Fatalf("SELECT DOUBLE literals: %v", err)
	}
	want := [][]string{{"10000000000", "0.0000000001", "-100000000000000000000"}}
	if !equalRows(result.rows, want) {
		t.Fatalf("SELECT DOUBLE literals = %#v, want %#v", result.rows, want)
	}
}
