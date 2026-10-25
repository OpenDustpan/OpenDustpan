package inventory_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// TestRemovalRoutesResolve takes a manual lock of each kind of object, then
// sends the routes that a refusal names, as refusal.RemovalRoutes builds them,
// to the inventory handler. The holder route removes the lock and the
// force-unlock route removes a new lock, so each route that a refusal names
// is a route that the handler serves.
func TestRemovalRoutesResolve(t *testing.T) {
	t.Parallel()

	good := `{"reason":"freeze for the audit"}`

	for _, tc := range []struct {
		name   string
		path   string
		target manuallock.Target
	}{
		{
			name: "unit", path: lockObjects()[0].path,
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"}),
		},
		{
			name: "collection", path: lockCollectionPath,
			target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
		},
		{name: "namespace", path: lockNamespacePath, target: manuallock.NamespaceTarget("acme")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newLockFixture(t, selectiveAuthorizer{deny: nil})
			routes := refusal.RemovalRoutes(tc.target)

			for _, step := range []struct {
				name, subject, method, path, body string
				want                              int
			}{
				{"lock", "alice", http.MethodPut, tc.path, good, http.StatusOK},
				{"the holder route", "alice", http.MethodDelete, routes.Remove, "", http.StatusNoContent},
				{"lock again", "alice", http.MethodPut, tc.path, good, http.StatusOK},
				{"the force-unlock route", "bob", http.MethodPost, routes.Force, "", http.StatusNoContent},
				{"no lock is left", "bob", http.MethodGet, tc.path, "", http.StatusNotFound},
			} {
				rec := f.do(t, step.subject, step.method, step.path, step.body)
				if rec.Code != step.want {
					t.Fatalf("%s: %s %s = %d %s, want %d", step.name, step.method, step.path, rec.Code,
						rec.Body.String(), step.want)
				}
			}
		})
	}
}
