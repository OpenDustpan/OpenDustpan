package outputsharetest_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
)

// TestSuiteOverAMemorySettingsStore runs the suite with a Settings in
// memory. The cases of the setting run on it. Each other case is pending, so
// it skips before it asks for a harness: the factory fails such a case.
func TestSuiteOverAMemorySettingsStore(t *testing.T) {
	t.Parallel()

	outputsharetest.Run(t, func(t *testing.T) outputsharetest.Harness {
		t.Helper()

		if !strings.HasPrefix(t.Name(), "TestSuiteOverAMemorySettingsStore/store:_") ||
			!strings.Contains(t.Name(), "setting") {
			t.Fatal("a pending case asked for a harness")
		}

		return newMemSettings().harness()
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
