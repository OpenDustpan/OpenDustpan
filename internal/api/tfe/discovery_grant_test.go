package tfe_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errGrantRead is the grant read error of the fakes of these tests.
var errGrantRead = errors.New("grant read failed")

// errWrongNamespace reports a question about an unexpected namespace.
var errWrongNamespace = errors.New("asked about an unexpected namespace")

// headerGrantAuthorizer denies every action but names the grant grt-test of
// a person on collection team-a of acme to DiscoveryGrantID. The empty id
// names no grant: the namespace stays hidden.
type headerGrantAuthorizer struct {
	id  string
	err error
}

func (headerGrantAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (headerGrantAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	ns, err := namespace.ParseName("acme")
	if err != nil {
		return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, err
	}

	return identity.Scope{
		Namespaces:  nil,
		Collections: []identity.Object{identity.CollectionObject(ns, "team-a")},
		Units:       nil,
		Principals:  nil,
	}, nil
}

func (a headerGrantAuthorizer) DiscoveryGrantID(
	_ context.Context, _ identity.Principal, ns namespace.Name,
) (string, error) {
	if ns.String() != "acme" {
		return "", errWrongNamespace
	}

	return a.id, a.err
}

// namespaceScopeAuthorizer denies every action and lists the namespace acme
// itself in the scope of the unit read actions. It cannot name a grant: a
// grant on the namespace alone must not open discovery.
type namespaceScopeAuthorizer struct{}

func (namespaceScopeAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (namespaceScopeAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{
		Namespaces:  []identity.Object{identity.NamespaceObject("acme")},
		Collections: nil,
		Units:       nil,
		Principals:  nil,
	}, nil
}

// TestDiscoveryAnswerNamesTheGrant proves that a discovery answer that a
// grant allowed carries the stable ID of the grant in the response header,
// on both discovery routes.
func TestDiscoveryAnswerNamesTheGrant(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"/api/v2/organizations/acme/entitlement-set",
		"/api/v2/organizations/acme/workspaces",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			handler := discoveryHandlerWithAudit(t, headerGrantAuthorizer{id: "grt-test-1", err: nil}, newTestAudit())

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("discovery answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			if got := rec.Header().Get(access.HeaderDiscoveryGrant); got != "grt-test-1" {
				t.Fatalf("%s = %q, want grt-test-1", access.HeaderDiscoveryGrant, got)
			}
		})
	}
}

// TestDiscoveryAnswerWithoutGrantNamesNoGrant proves that a discovery answer
// that no grant allowed carries no grant header: the authorizer allowed it,
// or the answer is the refusal of an unknown namespace.
func TestDiscoveryAnswerWithoutGrantNamesNoGrant(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		authz      identity.Authorizer
		want       int
		wantDenied int
	}{
		{"authorizer allowed", allowAll(), http.StatusOK, 0},
		{"no grant anywhere", stubAuthorizer{}, http.StatusNotFound, 1},
		{
			"grant read failed",
			headerGrantAuthorizer{id: "grt-test-1", err: errGrantRead},
			http.StatusInternalServerError,
			1,
		},
		{"no grant allows", headerGrantAuthorizer{id: "", err: nil}, http.StatusNotFound, 1},
		{"namespace-level scope only", namespaceScopeAuthorizer{}, http.StatusNotFound, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			entries := newTestAudit()
			handler := discoveryHandlerWithAudit(t, tc.authz, entries)

			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodGet, "/api/v2/organizations/acme/entitlement-set", http.NoBody,
			)
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("discovery answers %d, want %d", rec.Code, tc.want)
			}

			if got := rec.Header().Get(access.HeaderDiscoveryGrant); got != "" {
				t.Fatalf("%s = %q, want no grant header", access.HeaderDiscoveryGrant, got)
			}

			if got := countDeniedEntries(entries); got != tc.wantDenied {
				t.Fatalf("discovery writes %d denial entries, want %d", got, tc.wantDenied)
			}
		})
	}
}
