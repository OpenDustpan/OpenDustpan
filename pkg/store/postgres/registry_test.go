package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry/registrytest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// registryHarness returns a harness over a fresh store. The namespaces it makes
// are homed with failover version 1.
func registryHarness(base string) registrytest.StoreFactory {
	return func(t *testing.T) registrytest.Harness {
		t.Helper()

		return newRegistryHarness(t, open(t, base))
	}
}

func newRegistryHarness(t *testing.T, s *postgres.Store) registrytest.Harness {
	t.Helper()

	return registrytest.Harness{
		Store: s.Registry(),
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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	registrytest.RunStore(t, registryHarness(base))
}

func registryKeyHarness(base string) registrytest.KeyFactory {
	return func(t *testing.T) registrytest.KeyHarness {
		t.Helper()

		s := open(t, base)
		h := newRegistryHarness(t, s)

		return registrytest.KeyHarness{Store: s.RegistryKeys(), Namespace: h.Namespace, Entries: h.Entries}
	}
}

func TestRegistryKeyConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	registrytest.RunKeyStore(t, registryKeyHarness(base))
}
