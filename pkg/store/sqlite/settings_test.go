package sqlite_test

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// TestSettingStoreConformance runs the conformance suite of the settings port.
func TestSettingStoreConformance(t *testing.T) {
	t.Parallel()

	settingtest.Run(t, func(t *testing.T) settingtest.Harness {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s := openPath(t, path)

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
