package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/breakglass/breakglasstest"
)

// sinkDomain makes the namespaces that break-glass needs and reads the audit
// entries of a scope, so the suite can use it as its domain.
var _ breakglasstest.Domain = sinkDomain{s: nil}

// TestBreakGlassConformance runs the break-glass store suite against the
// SQLite adapter. The harness reads its namespaces and audit entries through
// sinkDomain, which makes a namespace, bumps its fence and lists its entries.
func TestBreakGlassConformance(t *testing.T) {
	t.Parallel()

	breakglasstest.Run(t, func(t *testing.T) breakglasstest.Harness {
		t.Helper()

		s := open(t)

		return breakglasstest.Harness{Store: s.BreakGlass(), Domain: sinkDomain{s: s}}
	})
}
