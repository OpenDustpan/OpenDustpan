package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge/connectiontest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// gitHostHarness returns a harness over a fresh store. The namespaces it makes
// have failover version 1.
func gitHostHarness(t *testing.T) connectiontest.Harness {
	t.Helper()

	s := open(t)

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

	connectiontest.Run(t, gitHostHarness)
}
