package mysql

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jonbaldie/database/internal/catalog"
)

func TestCatalogMutationFailureRequiresSentinel(t *testing.T) {
	unrelatedErr := errors.New("drop duplicate key index is not supported")
	fallback := sqlFailure{code: 1105, state: "HY000", message: "operation failed"}

	res := catalogMutationFailure(unrelatedErr, fallback)
	var f sqlFailure
	if !errors.As(res, &f) {
		t.Fatalf("expected sqlFailure, got %T: %v", res, res)
	}
	if f.code == 1062 {
		t.Fatalf("unrelated error was misclassified as duplicate entry 1062")
	}
	if f.message != unrelatedErr.Error() {
		t.Fatalf("expected message %q, got %q", unrelatedErr.Error(), f.message)
	}

	dupErr := fmt.Errorf("storage insert: %w", catalog.ErrDuplicateKey)
	resDup := catalogMutationFailure(dupErr, fallback)
	if !errors.As(resDup, &f) {
		t.Fatalf("expected sqlFailure, got %T: %v", resDup, resDup)
	}
	if f.code != 1062 {
		t.Fatalf("expected code 1062 for ErrDuplicateKey, got %d", f.code)
	}
	if f.state != "23000" {
		t.Fatalf("expected state 23000, got %q", f.state)
	}
}
