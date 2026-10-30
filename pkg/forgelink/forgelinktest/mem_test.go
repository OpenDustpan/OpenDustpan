package forgelinktest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forgelink/forgelinktest"
)

// TestMemConformance runs the suite against the reference adapter in memory, so
// that each case runs before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	forgelinktest.Run(t, func(*testing.T) forgelinktest.Harness {
		m := forgelinktest.NewMem()

		return forgelinktest.Harness{
			Store:        m,
			Principal:    func(_ *testing.T, name string) string { return name },
			AuditEntries: func(t *testing.T) []audit.Entry { return m.Entries(t.Context()) },
		}
	})
}
