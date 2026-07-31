package mutago_fixture

import "testing"

func TestPositiveAcceptsPositiveValue(t *testing.T) {
	if !Positive(1) {
		t.Fatal("expected a positive value to be accepted")
	}
}
