package mysql

import (
	"strings"
	"testing"
	"time"
)

func TestIssue222LikeMatchLongNoMatchIsBounded(t *testing.T) {
	value := strings.Repeat("a", 24)
	pattern := "%a%a%a%a%a%a%a%a%b"

	started := time.Now()
	if likeMatch(value, pattern, '\\', true) {
		t.Fatalf("likeMatch(%q, %q) unexpectedly matched", value, pattern)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("likeMatch(%q, %q) took %s; want less than 500ms", value, pattern, elapsed)
	}
}

func TestIssue222LikeMatchPreservesWildcardSemantics(t *testing.T) {
	tests := []struct {
		value, pattern string
		fold           bool
		want           bool
	}{
		{value: "abc", pattern: "a%", fold: true, want: true},
		{value: "abc", pattern: "a_c", fold: true, want: true},
		{value: "a%c", pattern: `a\%c`, fold: true, want: true},
		{value: "ABC", pattern: "a%", fold: true, want: true},
		{value: "ABC", pattern: "a%", fold: false, want: false},
		{value: "abc", pattern: "%a%b%c%", fold: true, want: true},
		{value: "abc", pattern: "%a%b%d%", fold: true, want: false},
	}

	for _, test := range tests {
		if got := likeMatch(test.value, test.pattern, '\\', test.fold); got != test.want {
			t.Errorf("likeMatch(%q, %q, fold=%v) = %v, want %v", test.value, test.pattern, test.fold, got, test.want)
		}
	}
}
