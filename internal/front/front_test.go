package front

import "testing"

func TestCurrentDoesNotPanic(t *testing.T) {
	name := Current()
	if len(name) > 512 {
		t.Fatalf("name too long: %d", len(name))
	}
}
