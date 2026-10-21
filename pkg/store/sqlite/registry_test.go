package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry/registrytest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// registryHarness returns a harness over a fresh store. The namespaces it makes
// are homed with failover version 1.
func registryHarness(t *testing.T) registrytest.Harness {
	t.Helper()

	return newRegistryHarness(t, open(t))
}

func newRegistryHarness(t *testing.T, s *sqlite.Store) registrytest.Harness {
	t.Helper()

	return registrytest.Harness{
		Store: s.Registry(),
		Keys:  s.RegistryKeys(),
		Namespace: func(t *testing.T, name namespace.Name) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, name)
		},
		Entries: func(t *testing.T, name namespace.Name) []audit.Entry {
			t.Helper()

			return allAuditEntries(t, s, name)
		},
	}
}

func TestRegistryConformance(t *testing.T) {
	t.Parallel()

	registrytest.RunStore(t, registryHarness)
}

func registryKeyHarness(t *testing.T) registrytest.KeyHarness {
	t.Helper()

	s := open(t)
	h := newRegistryHarness(t, s)

	return registrytest.KeyHarness{Store: s.RegistryKeys(), Namespace: h.Namespace, Entries: h.Entries}
}

func TestRegistryKeyConformance(t *testing.T) {
	t.Parallel()

	registrytest.RunKeyStore(t, registryKeyHarness)
}
