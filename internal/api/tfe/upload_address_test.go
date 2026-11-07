package tfe_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// uploadAddressGate is a tfe AddressGate stub that counts calls, records the
// namespace and answers err.
type uploadAddressGate struct {
	calls int
	ns    namespace.Name
	err   error
}

// CheckAddress implements tfe.AddressGate.
func (g *uploadAddressGate) CheckAddress(_ context.Context, ns namespace.Name) error {
	g.calls++
	g.ns = ns

	return g.err
}

// newUploadAddressHandler returns a production Handler over ws with the
// address gate set.
func newUploadAddressHandler(
	t *testing.T, ws tfe.WorkspaceService, gate *bodygate.Gate, addressGate tfe.AddressGate,
) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    addressGate,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     ws,
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           gate,
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

// unreadBody fails the test when read: the address gate must refuse before
// any body byte is read.
type unreadBody struct {
	t *testing.T
}

// Read implements io.Reader.
func (b unreadBody) Read([]byte) (int, error) {
	b.t.Helper()
	b.t.Error("upload body read before the address check")

	return 0, io.EOF
}

// uploadPUT builds a PUT to target with a body that fails the test when read.
func uploadPUT(t *testing.T, target string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(t.Context(), http.MethodPut, target, unreadBody{t: t})
}

// TestUploadAddressGateDenies proves a denying address gate answers 403 on
// both upload routes before any body byte is read.
func TestUploadAddressGateDenies(t *testing.T) {
	t.Parallel()

	validTok, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	valid := validTok.Reveal()
	gate := &uploadAddressGate{calls: 0, ns: "", err: identity.ErrDenied}
	handler := newUploadAddressHandler(
		t, checkGateWorkspaces{stubWorkspaces: stubWorkspaces{}, t: t, valid: valid}, bodygate.New(0), gate,
	)

	for _, target := range []string{
		"/api/v2/state-uploads/sv-new123/" + valid,
		"/api/v2/json-state-uploads/sv-new123/" + valid,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, uploadPUT(t, target))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s answers %d, want %d: %s", target, rec.Code, http.StatusForbidden, rec.Body.String())
		}
	}

	if gate.calls != 2 {
		t.Fatalf("gate calls = %d, want 2", gate.calls)
	}

	if gate.ns != "acme" {
		t.Fatalf("gate namespace = %q, want acme", gate.ns)
	}
}

// TestUploadAddressGateSkippedOnBadToken proves a bad capability answers the
// normal capability error without calling the address gate: the capability
// check runs first, so a caller with a bad token learns nothing about an
// allowlist.
func TestUploadAddressGateSkippedOnBadToken(t *testing.T) {
	t.Parallel()

	validTok, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	valid := validTok.Reveal()
	bad := wrongToken(t)
	gate := &uploadAddressGate{calls: 0, ns: "", err: identity.ErrDenied}
	handler := newUploadAddressHandler(
		t, checkGateWorkspaces{stubWorkspaces: stubWorkspaces{}, t: t, valid: valid}, bodygate.New(0), gate,
	)

	for _, target := range []string{
		"/api/v2/state-uploads/sv-new123/" + bad,
		"/api/v2/json-state-uploads/sv-new123/" + bad,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, uploadRequest(t, target, "state-content"))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s answers %d, want %d: %s", target, rec.Code, http.StatusForbidden, rec.Body.String())
		}
	}

	if gate.calls != 0 {
		t.Fatalf("gate calls with a bad capability = %d, want 0", gate.calls)
	}
}

// TestUploadAddressGateAllows proves an allowing gate changes nothing: both
// uploads answer 200 as before.
func TestUploadAddressGateAllows(t *testing.T) {
	t.Parallel()

	validTok, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	valid := validTok.Reveal()
	gate := &uploadAddressGate{calls: 0, ns: "", err: nil}
	handler := newUploadAddressHandler(
		t, checkGateWorkspaces{stubWorkspaces: stubWorkspaces{}, t: t, valid: valid}, bodygate.New(0), gate,
	)

	for _, target := range []string{
		"/api/v2/state-uploads/sv-new123/" + valid,
		"/api/v2/json-state-uploads/sv-new123/" + valid,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, uploadRequest(t, target, `{"version":4}`))

		if rec.Code != http.StatusOK {
			t.Fatalf("%s answers %d, want %d: %s", target, rec.Code, http.StatusOK, rec.Body.String())
		}
	}

	if gate.calls != 2 {
		t.Fatalf("gate calls = %d, want 2", gate.calls)
	}
}
