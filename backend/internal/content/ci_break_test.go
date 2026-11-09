package content

import "testing"

// Deliberately broken (PLAN step 15b): checks that CI turns red.
// Bad formatting fails `make lint`; the failing test fails `make test`.
// Delete this file after the check.
func TestCIDeliberateFailure(t *testing.T)  {
	if 1+1   != 3 {
		t.Fatal("deliberate failure: CI must turn red")
	}
}
