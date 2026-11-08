//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// coverageIPAllowlistStore holds no rule: the route table needs an IP
// allowlist service, and the coverage gate never serves a request.
type coverageIPAllowlistStore struct{}

func (coverageIPAllowlistStore) NamespaceList(_ context.Context, _ namespace.Name) (ipallow.List, error) {
	return ipallow.List{}, nil
}

func (coverageIPAllowlistStore) SetNamespaceList(
	_ context.Context, _ namespace.Name, _ ipallow.List, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (coverageIPAllowlistStore) NamespacesWithList(context.Context) ([]namespace.Name, error) {
	return nil, nil
}

// coverageIPAllowlists returns the IP allowlist service of the route table.
func coverageIPAllowlists(tb testing.TB) *ipallow.Service {
	tb.Helper()

	svc, err := ipallow.NewService(ipallow.Config{
		Store: coverageIPAllowlistStore{}, Namespaces: coverageNamespaces(tb), Now: nil, Enabled: true,
	})
	if err != nil {
		tb.Fatalf("ip allowlist service: %v", err)
	}

	return svc
}

// namespacePrincipalCoveringTest names the test that proves the routes of
// the principals of a namespace instead of a CLI: no CLI calls them.
const namespacePrincipalCoveringTest = "TestNamespacePrincipals"

// namespacePrincipalAllowEntries returns the operator and console routes of
// the principals of a namespace. The harness steps of
// namespacePrincipalCoveringTest drive each one through the real binary on
// each store.
func namespacePrincipalAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages the principals of a namespace. " +
		"The harness step calls it through the real binary and checks the answer."

	const (
		list = "/api/server/namespaces/{namespace}/principals"
		one  = list + "/{id}"
	)

	routes := []struct{ method, path string }{
		{http.MethodPost, list},
		{http.MethodGet, list},
		{http.MethodGet, one},
		{http.MethodPatch, one},
		{http.MethodDelete, one},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: namespacePrincipalCoveringTest, Reason: reason,
		})
	}

	return out
}

// ipAllowlistCoveringTest names the test that proves the IP allowlist routes
// instead of a CLI: no CLI manages IP allowlists.
const ipAllowlistCoveringTest = "TestIPAllowlists"

// ipAllowlistAllowEntries returns the operator and console routes of the IP
// allowlists of the namespaces. The harness steps of
// ipAllowlistCoveringTest drive each one through the real binary on each store.
func ipAllowlistAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages IP allowlists. " +
		"The harness step calls it through the real binary and checks the answer."

	const list = "/api/server/namespaces/{namespace}/ip-allowlist"

	routes := []struct{ method, path string }{
		{http.MethodGet, list},
		{http.MethodPut, list},
		{http.MethodDelete, list},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: ipAllowlistCoveringTest, Reason: reason,
		})
	}

	return out
}

// namespaceCoveringTest names the test that proves the namespace create route
// instead of a CLI: no CLI version in the matrix calls it.
const namespaceCoveringTest = "TestCLIs (checkNamespaceCreateThroughAPI)"

// namespaceAllowEntries returns the operator namespace routes that no CLI
// calls. The harness creates the first namespace through the create on an
// empty installation, and the scenario steps prove the duplicate, invalid
// and unauthenticated cases. The harness lists the namespaces through the
// list, which no CLI calls either.
func namespaceAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodPost, Path: "/api/server/namespaces",
			CoveringTest: namespaceCoveringTest,
			Reason: "Operator route: no CLI creates a namespace. The harness " +
				"creates the first one on an empty installation and the scenario " +
				"checks the duplicate, invalid and unauthenticated cases.",
		},
		{
			Method: http.MethodGet, Path: "/api/server/namespaces",
			CoveringTest: "TestRolesAndGrants",
			Reason: "Operator and console route: no CLI lists namespaces. The harness " +
				"step lists them as the admin and as the member and pages through them.",
		},
	}
}
