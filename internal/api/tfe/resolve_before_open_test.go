package tfe_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// openCountingWorkspaces serves the stub workspace. Its ReadState is the call
// that opens sealed outputs in the real service, so it counts each call and
// fails the way the real service fails for a sealed version. Its
// StateVersionWorkspace is the lookup that opens nothing.
type openCountingWorkspaces struct {
	stubWorkspaces

	opens *atomic.Int32
}

// The state version IDs that openCountingWorkspaces knows. Each known ID
// belongs to ws-test123.
const (
	svDecryptFault  = "sv-decrypt123"
	svKeyStoreFault = "sv-keystore123"
	svPending       = "sv-pending123"
)

func (o openCountingWorkspaces) ReadState(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	o.opens.Add(1)

	switch id.String() {
	case svDecryptFault:
		return workspace.StateVersion{}, workspace.ErrSealedContent
	case svKeyStoreFault:
		return workspace.StateVersion{}, errTestInternal
	default:
		return o.stubWorkspaces.ReadState(ctx, id)
	}
}

func (o openCountingWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	switch id.String() {
	case svDecryptFault, svKeyStoreFault:
		return workspace.ID("ws-test123"), nil
	case svPending:
		return "", workspace.ErrStateVersionNotFound
	default:
		return locateVia(ctx, o.stubWorkspaces.ReadState, id)
	}
}

func newOpenCountingHandler(t *testing.T, authz identity.Authorizer) (*tfe.Handler, *atomic.Int32) {
	t.Helper()

	opens := &atomic.Int32{}
	ws := openCountingWorkspaces{stubWorkspaces: stubWorkspaces{}, opens: opens}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     ws,
		OutputChecker:  newTestOutputChecker(t, authz, ws),
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
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

	return handler, opens
}

// TestStateVersionRoutesAuthorizeBeforeOpeningOutputs proves, for a caller
// without the read right, that no state version route opens a sealed output,
// and that the answer for each version that cannot be opened is the answer
// for an unknown ID: same status, same body, same headers. It fails when the
// resolver reads the version through a call that opens outputs.
func TestStateVersionRoutesAuthorizeBeforeOpeningOutputs(t *testing.T) {
	t.Parallel()

	denied := selectiveAuthorizer{deny: map[identity.Action]bool{
		identity.ActionUnitRead: true, identity.ActionUnitReadOutputs: true,
	}}
	handler, opens := newOpenCountingHandler(t, denied)

	t.Cleanup(func() {
		if n := opens.Load(); n != 0 {
			t.Errorf("the server opened outputs %d times before authorization, want 0", n)
		}
	})

	t.Run("rows", func(t *testing.T) {
		t.Parallel()

		for _, suffix := range []string{"", "/download", "/resources"} {
			reference := httptest.NewRecorder()
			handler.ServeHTTP(reference, stateRequest(t, "/api/v2/state-versions/sv-unknown123"+suffix))

			if reference.Code != http.StatusNotFound {
				t.Fatalf("unknown ID %q answered %d, want 404", suffix, reference.Code)
			}

			for _, id := range []string{
				"sv-test123", svDecryptFault, svKeyStoreFault, svPending, "sv-other-namespace123", "not-an-id", "sv-%00",
			} {
				t.Run(id+suffix, func(t *testing.T) {
					t.Parallel()

					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, stateRequest(t, "/api/v2/state-versions/"+id+suffix))

					assertSameAnswer(t, reference, rec)
				})
			}
		}
	})
}

// assertSameAnswer fails when got differs from want in status, body or
// headers.
func assertSameAnswer(t *testing.T, want, got *httptest.ResponseRecorder) {
	t.Helper()

	if got.Code != want.Code {
		t.Errorf("status = %d, want %d", got.Code, want.Code)
	}

	if got.Body.String() != want.Body.String() {
		t.Errorf("body = %q, want %q", got.Body.String(), want.Body.String())
	}

	for name, values := range want.Header() {
		if g := got.Header().Values(name); len(g) != len(values) || (len(g) > 0 && g[0] != values[0]) {
			t.Errorf("header %s = %v, want %v", name, g, values)
		}
	}

	if len(got.Header()) != len(want.Header()) {
		t.Errorf("headers = %v, want %v", got.Header(), want.Header())
	}
}
