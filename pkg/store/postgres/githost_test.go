package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge/connectiontest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// gitHostHarness returns a harness over a fresh store. The namespaces it makes
// are homed with failover version 1.
func gitHostHarness(base string) connectiontest.Factory {
	return func(t *testing.T) connectiontest.Harness {
		t.Helper()

		return newGitHostHarness(t, open(t, base))
	}
}

func newGitHostHarness(t *testing.T, s *postgres.Store) connectiontest.Harness {
	t.Helper()

	return connectiontest.Harness{
		Store: s.GitHost(),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Audit: func(t *testing.T, scope namespace.Name) []audit.Entry {
			t.Helper()

			return allAuditEntries(t, s, scope)
		},
	}
}

func TestGitHostConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	connectiontest.Run(t, gitHostHarness(base))
}
