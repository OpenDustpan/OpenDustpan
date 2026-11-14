package tfe_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ipBlockedCollectionAuthorizer refuses namespace:read with an IP allowlist
// refusal that also matches identity.ErrDenied, as the allowlist gate does,
// and reports a grant on one collection of acme for the read actions.
type ipBlockedCollectionAuthorizer struct{}

func (ipBlockedCollectionAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionNamespaceRead {
		return fmt.Errorf(
			"%w: %w: %s may not %s on %q",
			identity.ErrDenied,
			ipallow.ErrAddressNotAllowed,
			p.Subject,
			action,
			obj.String(),
		)
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (ipBlockedCollectionAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	ns, err := namespace.ParseName("acme")
	if err != nil {
		return identity.Scope{}, err
	}

	return identity.Scope{
		Namespaces: nil, Collections: []identity.Object{identity.CollectionObject(ns, "team-a")}, Units: nil,
		Principals: nil,
	}, nil
}

// discoveryHandlerWithAudit returns a production Handler over authz that
// writes its audit entries to entries.
func discoveryHandlerWithAudit(
	t *testing.T, authz identity.Authorizer, entries *testAudit,
) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  newTestOutputChecker(t, authz, stubWorkspaces{}),
		BaseURL:        baseurl.Resolver{},
		Audit:          entries,
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// countDeniedEntries counts the denied entries of entries.
func countDeniedEntries(entries *testAudit) int {
	entries.mu.Lock()
	defer entries.mu.Unlock()

	count := 0

	for i := range entries.entries {
		if entries.entries[i].Outcome == audit.OutcomeDenied {
			count++
		}
	}

	return count
}

// TestDiscoveryIPAllowlistRefusesCollectionGrant proves a request from an
// address outside the IP allowlist 198.51.100.0/24 on namespace acme cannot
// use a collection grant to discover the namespace: the entitlement set and
// the workspace list both answer 403.
func TestDiscoveryIPAllowlistRefusesCollectionGrant(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"/api/v2/organizations/acme/entitlement-set",
		"/api/v2/organizations/acme/workspaces",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			handler := discoveryHandlerWithAudit(t, ipBlockedCollectionAuthorizer{}, newTestAudit())

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("GET %s outside the allowlist answers %d, want %d", target, rec.Code, http.StatusForbidden)
			}
		})
	}
}

// TestDiscoveryWritesNoDenialForServedGrant proves a served discovery request
// of a holder of a collection grant writes no denial entry, and a caller with
// no grant in the namespace writes exactly one.
func TestDiscoveryWritesNoDenialForServedGrant(t *testing.T) {
	t.Parallel()

	served := newTestAudit()
	servedHandler := discoveryHandlerWithAudit(t, collectionGrantAuthorizer{}, served)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v2/organizations/acme/entitlement-set", http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	servedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("entitlement set of a collection grant holder answers %d, want %d", rec.Code, http.StatusOK)
	}

	if got := countDeniedEntries(served); got != 0 {
		t.Fatalf("served discovery request writes %d denial entries, want 0", got)
	}

	denied := newTestAudit()
	deniedHandler := discoveryHandlerWithAudit(t, stubAuthorizer{}, denied)

	req = httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v2/organizations/acme/entitlement-set", http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	deniedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("entitlement set with no grant answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	if got := countDeniedEntries(denied); got != 1 {
		t.Fatalf("refused discovery request writes %d denial entries, want 1", got)
	}
}

// everyObjectCollectionAuthorizer denies every action but reports an
// every-object scope: without the gated namespace check this scope alone
// would open discovery.
type everyObjectCollectionAuthorizer struct{}

func (everyObjectCollectionAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (everyObjectCollectionAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Every(), nil
}

// TestScopeNeverCoversEveryObject proves an every-object scope alone opens no
// discovery route: the caller is denied namespace:read and holds no named
// collection or unit in acme, so the entitlement set answers 404, never 200.
func TestScopeNeverCoversEveryObject(t *testing.T) {
	t.Parallel()

	handler := discoveryHandlerWithAudit(t, everyObjectCollectionAuthorizer{}, newTestAudit())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/v2/organizations/acme/entitlement-set", http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("entitlement set with only an every-object scope answers %d, want %d", rec.Code, http.StatusNotFound)
	}
}
