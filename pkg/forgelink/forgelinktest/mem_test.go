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

	forgelinktest.Run(t, func(t *testing.T) forgelinktest.Harness {
		t.Helper()

		m := forgelinktest.NewMem()

		return forgelinktest.Harness{
			Store: m,
			Principal: func(t *testing.T, name string) string {
				t.Helper()

				return "prn-" + name
			},
			AuditEntries: func(t *testing.T) []audit.Entry {
				t.Helper()

				return m.Entries(t.Context())
			},
		}
	})
}
