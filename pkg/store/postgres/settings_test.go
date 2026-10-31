package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// TestSettingStoreConformance runs the conformance suite of the settings port
// against PostgreSQL.
func TestSettingStoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	settingtest.Run(t, func(t *testing.T) settingtest.Harness {
		t.Helper()

		s := openURL(t, isolatedURL(t, base))

		return settingtest.Harness{
			Store: s.SettingStore(),
			Namespace: func(t *testing.T, name string) namespace.Fence {
				t.Helper()

				return ensureNamespace(t, s, namespace.Name(name))
			},
			Audit: func(t *testing.T, scope namespace.Name) []audit.Entry {
				t.Helper()

				return allAuditEntries(t, s, scope)
			},
		}
	})
}
