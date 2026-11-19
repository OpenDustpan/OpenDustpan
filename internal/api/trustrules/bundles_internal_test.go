package trustrules

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// guardBundles is the bundle port of the guard tests of this package. It
// counts each read and each delete, and serves the one bundle that it holds.
// No save or rotate runs in these tests, so those methods stay on the nil
// embedded port and would fail the test if a path reached them.
type guardBundles struct {
	trustrule.BundleStore

	bundle  trustrule.Bundle
	gets    int
	deletes int
}

func (s *guardBundles) GetBundle(context.Context, namespace.Name) (trustrule.Bundle, error) {
	s.gets++

	return s.bundle, nil
}

func (s *guardBundles) DeleteBundleVersion(
	_ context.Context, _ namespace.Name, _ int64, _ namespace.Fence, _ audit.Entry,
) error {
	s.deletes++

	return nil
}

// guardRules holds the rule port that no test of this file reaches. The
// service needs a port, and the nil embedded interface fails a test that
// calls a method on it.
type guardRules struct {
	trustrule.Store
}

// guardNamespace serves each namespace as itself, with every field set: the
// service reads the fields it needs, and no test of this file writes one.
func guardNamespace(name namespace.Name) namespace.Namespace {
	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: guardNow(), Name: name,
		PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// guardNamespaces serves each namespace as itself.
type guardNamespaces struct{}

func (guardNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return guardNamespace(name), nil
}

func (guardNamespaces) WritableHere(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return guardNamespace(name), nil
}

// guardPrincipals serves no principal: no test of this file reads one.
type guardPrincipals struct{}

func (guardPrincipals) GetPrincipal(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, principal.ErrNotFound
}

// guardNow is the fixed clock of the guard tests, so that no test reads the
// wall clock.
func guardNow() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

// guardCaller returns the console caller of the guard tests, with every field
// set: no test of this file reads a field of it.
func guardCaller() identity.Principal {
	return identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
}

// newGuardHandler returns a handler over the counting bundle port, and the
// port. The tests call the handler steps directly, so no route wrapper runs:
// the hostile input reaches this layer as if every other check had failed.
func newGuardHandler(t *testing.T) (*Handler, *guardBundles) {
	t.Helper()

	bundles := &guardBundles{
		BundleStore: nil,
		bundle: trustrule.Bundle{
			ID: trustrule.NewBundleID(), Namespace: "acme", Active: 0, Versions: nil,
			Revision: 0, CreatedAt: guardNow(),
		},
		gets:    0,
		deletes: 0,
	}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: guardRules{Store: nil}, Bundles: bundles, Namespaces: guardNamespaces{}, Principals: guardPrincipals{},
		Authorizer: enforceAllowAll{Authorizer: nil}, Reach: nil, InstallationURLs: nil, OwnIssuer: nil,
		CheckURL: nil, Sessions: nil, Now: guardNow,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return &Handler{
		namespaces:     nil,
		rules:          svc,
		exchanger:      nil,
		mtls:           nil,
		mtlsGuard:      nil,
		onMtlsRefused:  nil,
		authn:          nil,
		sessions:       nil,
		guard:          nil,
		audit:          nil,
		onAuditFailure: nil,
		logger:         slog.New(slog.DiscardHandler),
		now:            guardNow,
		mux:            nil,
		routes:         nil,
	}, bundles
}

// TestBundlePathRefusesAnIDThatIsNotABundleID checks the guard of the form of
// the bundle ID of the path, at the handler alone: a path ID that is not a
// bundle ID names no bundle, and the store is not read.
//
// Defense in depth: the row check of the store serves only a row whose ID is
// a bundle ID, so this text never answers a read. The guard stays so that a
// path ID of any other form never reaches the store. Without it, the read
// runs, and the refusal depends on the row that the read finds: the count of
// the reads proves the guard.
func TestBundlePathRefusesAnIDThatIsNotABundleID(t *testing.T) {
	t.Parallel()

	h, bundles := newGuardHandler(t)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	r.SetPathValue("bundle", "not-a-bundle-id")

	_, err := h.bundleOfPath(r, identity.NamespaceObject("acme"))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("bundleOfPath of a bad ID = %v, want ErrNotFound", err)
	}

	if bundles.gets != 0 {
		t.Fatalf("bundle reads = %d, want 0: the ID that is not a bundle ID reached the store", bundles.gets)
	}
}

// TestBundleVersionDeleteRefusesAVersionBelowOne checks the guard of the
// version of the path, at the handler alone: a version below 1 names no
// version, and the store is not read. Without the guard, the delete runs with
// the version 0, and the count of the deletes proves the guard.
func TestBundleVersionDeleteRefusesAVersionBelowOne(t *testing.T) {
	t.Parallel()

	h, bundles := newGuardHandler(t)

	// A fresh console session, so that only the version guard refuses the
	// request: without the guard, the write runs and reaches the store.
	ctx := sessionauth.WithSession(t.Context())
	ctx = sessionauth.WithSignIn(ctx, sessionauth.SignIn{At: guardNow(), Provider: "local"})

	r := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/x", http.NoBody)
	r.SetPathValue("bundle", string(bundles.bundle.ID))
	r.SetPathValue("version", "0")

	rec := httptest.NewRecorder()
	h.deleteBundleVersion(rec, r, guardCaller(), identity.NamespaceObject("acme"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete of version 0 = %d %s, want 404", rec.Code, rec.Body.String())
	}

	if bundles.deletes != 0 {
		t.Fatalf("bundle version deletes = %d, want 0: the version 0 reached the store", bundles.deletes)
	}
}
