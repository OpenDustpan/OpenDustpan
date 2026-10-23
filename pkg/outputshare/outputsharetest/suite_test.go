package outputsharetest_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/outputshare/outputsharetest"
)

// TestSuiteOverAMemorySettingsStore runs the suite with a Settings in
// memory. The cases of the setting run on it. A pending case skips before it
// asks for a harness. A case of the checker, of the States port or of the
// Outputs port needs the stores of the tools, so the factory skips it: the
// server tests run it.
func TestSuiteOverAMemorySettingsStore(t *testing.T) {
	t.Parallel()

	outputsharetest.Run(t, func(t *testing.T) outputsharetest.Harness {
		t.Helper()

		if !strings.HasPrefix(t.Name(), "TestSuiteOverAMemorySettingsStore/store:_") ||
			!strings.Contains(t.Name(), "setting") {
			for _, prefix := range []string{"checker:", "latest:", "client-encrypted:", "states:", "outputs:"} {
				if strings.HasPrefix(strings.TrimPrefix(t.Name(), "TestSuiteOverAMemorySettingsStore/"), prefix) {
					t.Skip("the case needs the stores of the tools")
				}
			}

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
