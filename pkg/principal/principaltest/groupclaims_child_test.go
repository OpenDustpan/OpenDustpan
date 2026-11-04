package principaltest_test

import (
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/testchild"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
)

// designOnlyChild selects the suite that the child process runs with a
// fixture.
const designOnlyChild = "OPENDUSTPAN_PRINCIPALTEST_DESIGN_ONLY"

// TestRunGroupMapperRefusesAFixture proves that RunGroupMapper fails when it
// receives a fixture. The t.Fatal of the suite cannot be caught in the same
// test, so the test runs itself in a child process and reads its output.
func TestRunGroupMapperRefusesAFixture(t *testing.T) {
	t.Parallel()

	if os.Getenv(designOnlyChild) == "mapper" {
		principaltest.RunGroupMapper(t, func(*testing.T) principaltest.GroupMapperFixture {
			return principaltest.GroupMapperFixture{Mapper: nil, Store: nil, SetMapping: nil}
		})

		return
	}

	requireDesignOnlyRefusal(t, "mapper", "RunGroupMapper")
}

// TestRunGroupClaimsRefusesAFixture proves that RunGroupClaims fails when it
// receives a fixture. See TestRunGroupMapperRefusesAFixture.
func TestRunGroupClaimsRefusesAFixture(t *testing.T) {
	t.Parallel()

	if os.Getenv(designOnlyChild) == "claims" {
		principaltest.RunGroupClaims(t, func(*testing.T) principaltest.GroupClaimsFixture {
			return principaltest.GroupClaimsFixture{
				Applier: nil,
				Store:   nil,
				Mapping: principaltest.GroupMapperFixture{Mapper: nil, Store: nil, SetMapping: nil},
			}
		})

		return
	}

	requireDesignOnlyRefusal(t, "claims", "RunGroupClaims")
}

// requireDesignOnlyRefusal runs the calling test in a child process with the
// suite selected, and fails the test unless the child failed with the
// design-only refusal of the named suite.
func requireDesignOnlyRefusal(t *testing.T, mode, suite string) {
	t.Helper()

	out, err := testchild.Run(t, "^"+t.Name()+"$", designOnlyChild+"="+mode)
	if err == nil {
		t.Fatalf("%s passed with a fixture, want failure:\n%s", suite, out)
	}

	if !strings.Contains(out, "is design only") {
		t.Fatalf("child output does not hold the text %q:\n%s", "is design only", out)
	}

	if !strings.Contains(out, suite+" is design only") {
		t.Fatalf("child output does not name %s:\n%s", suite, out)
	}
}
