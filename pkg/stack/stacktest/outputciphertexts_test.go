package stacktest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// TestOutputCiphertextsWaitsForAnAdapter runs the suite with a factory that no
// case may call: each case is pending, so each case skips before it asks for
// a harness.
func TestOutputCiphertextsWaitsForAnAdapter(t *testing.T) {
	t.Parallel()

	stacktest.RunOutputCiphertexts(t, func(t *testing.T) stacktest.OutputCiphertextsHarness {
		t.Helper()
		t.Fatal("a pending case asked for a harness")

		return stacktest.OutputCiphertextsHarness{Port: nil, Save: nil}
	})
}

// TestOutputCiphertextsCaseNamesAreUnique proves that each case has a name of
// its own.
func TestOutputCiphertextsCaseNamesAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, name := range stacktest.OutputCiphertextsCaseNames() {
		if seen[name] {
			t.Fatalf("case %q is in the suite twice", name)
		}

		seen[name] = true
	}

	if len(seen) == 0 {
		t.Fatal("the suite has no case")
	}
}
