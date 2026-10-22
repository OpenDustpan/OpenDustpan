package outputsharetest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
)

// TestSuiteWaitsForAnAdapter runs the suite with a factory that no case may
// call: each case is pending, so each case skips before it asks for a
// harness.
func TestSuiteWaitsForAnAdapter(t *testing.T) {
	t.Parallel()

	outputsharetest.Run(t, func(t *testing.T) outputsharetest.Harness {
		t.Helper()
		t.Fatal("a pending case asked for a harness")

		return outputsharetest.Harness{
			Store: nil, Checker: nil, Namespace: nil, Unit: nil, UnitFault: nil, Authority: nil,
			Grant: nil, Job: nil, Disable: nil, Audited: nil, StoreRaw: nil,
		}
	})
}

// TestCaseNamesAreUnique proves that each case has a name of its own, so a
// skip or a failure names one behavior.
func TestCaseNamesAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, name := range outputsharetest.Names() {
		if seen[name] {
			t.Fatalf("case %q is in the suite twice", name)
		}

		seen[name] = true
	}

	if len(seen) == 0 {
		t.Fatal("the suite has no case")
	}
}
