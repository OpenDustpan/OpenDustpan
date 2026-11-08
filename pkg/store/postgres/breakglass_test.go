package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/breakglass/breakglasstest"
)

// TestBreakGlassConformance runs the break-glass store suite against the
// PostgreSQL adapter. The harness reads its namespaces and audit entries
// through sinkDomain, which makes a namespace, bumps its fence and lists its
// entries.
func TestBreakGlassConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	breakglasstest.Run(t, func(t *testing.T) breakglasstest.Harness {
		t.Helper()

		s := open(t, base)

		return breakglasstest.Harness{Store: s.BreakGlass(), Domain: sinkDomain{s: s}}
	})
}
