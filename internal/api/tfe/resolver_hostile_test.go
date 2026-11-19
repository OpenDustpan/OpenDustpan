package tfe_test

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// otherNamespaceWorkspaces adds the workspace ws-other123 in the namespace
// other, with the state version sv-other123, to the stub workspaces.
type otherNamespaceWorkspaces struct {
	stubWorkspaces
}

func (o otherNamespaceWorkspaces) GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	if id.String() != "ws-other123" {
		return o.stubWorkspaces.GetByID(ctx, id)
	}

	ws, err := o.stubWorkspaces.GetByID(ctx, workspace.ID("ws-test123"))
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.ID = id
	ws.Ref = workspace.Ref{Namespace: namespace.Name("other"), Name: ws.Ref.Name}

	return ws, nil
}

func (o otherNamespaceWorkspaces) ReadState(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if id.String() != "sv-other123" {
		return o.stubWorkspaces.ReadState(ctx, id)
	}

	sv := stubStateVersion()
	sv.ID = id
	sv.Workspace = workspace.ID("ws-other123")

	return sv, nil
}

// newResolverHandler returns a handler over the stub workspaces and the
// workspace of another namespace, with authz as the authorizer.
func newResolverHandler(t *testing.T, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	workspaces := otherNamespaceWorkspaces{stubWorkspaces: stubWorkspaces{}}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     workspaces,
		OutputChecker:  newTestOutputChecker(t, authz, workspaces),
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

	return handler
}

// resolverAnswer is the whole answer of one request: status, body and the
// headers that do not carry a per-request value.
type resolverAnswer struct {
	code   int
	body   string
	header http.Header
}

func resolverAnswerOf(rec *httptest.ResponseRecorder) resolverAnswer {
	h := rec.Header().Clone()
	h.Del("X-Request-ID")
	h.Del("Date")

	return resolverAnswer{code: rec.Code, body: rec.Body.String(), header: h}
}

// TestResolversAnswerADeniedCallerTheSame proves, for each route that
// resolves an object from an ID, that a caller without any read action gets
// one answer (status, body and headers) for an existing object, an unknown
// ID, an object of another namespace, an ID of the wrong kind and a
// malformed ID: a 404 JSON:API document. A caller with the action still
// reads the existing object, so the rows do not pass for lack of a route.
func TestResolversAnswerADeniedCallerTheSame(t *testing.T) {
	t.Parallel()

	routes := []struct {
		name     string
		method   string
		pattern  string // %s is the ID
		body     string
		existing string
		variants map[string]string
	}{
		{
			name: "workspace", method: http.MethodGet, pattern: "/api/v2/workspaces/%s", body: "",
			existing: "ws-test123",
			variants: map[string]string{
				"unknown": "ws-missing", "other namespace": "ws-other123",
				"state version ID": "sv-test123", "malformed": "%00..%2F..", "empty-like": "ws-",
			},
		},
		{
			name: "workspace outputs", method: http.MethodGet,
			pattern: "/api/v2/workspaces/%s/current-state-version-outputs", body: "",
			existing: "ws-test123",
			variants: map[string]string{
				"unknown": "ws-missing", "other namespace": "ws-other123",
				"state version ID": "sv-test123", "malformed": "%00..%2F..",
			},
		},
		{
			name: "lock", method: http.MethodPost, pattern: "/api/v2/workspaces/%s/actions/lock", body: `{"data":{}}`,
			existing: "ws-test123",
			variants: map[string]string{
				"unknown": "ws-missing", "other namespace": "ws-other123",
				"state version ID": "sv-test123", "malformed": "%00..%2F..",
			},
		},
		{
			name: "state version", method: http.MethodGet, pattern: "/api/v2/state-versions/%s", body: "",
			existing: "sv-test123",
			variants: map[string]string{
				"unknown": "sv-missing", "other namespace": "sv-other123",
				"workspace ID": "ws-test123", "malformed": "%00..%2F..",
			},
		},
		{
			name: "state version download", method: http.MethodGet,
			pattern: "/api/v2/state-versions/%s/download", body: "",
			existing: "sv-test123",
			variants: map[string]string{
				"unknown": "sv-missing", "other namespace": "sv-other123",
				"workspace ID": "ws-test123", "malformed": "%00..%2F..",
			},
		},
		{
			name: "state version resources", method: http.MethodGet,
			pattern: "/api/v2/state-versions/%s/resources", body: "",
			existing: "sv-test123",
			variants: map[string]string{
				"unknown": "sv-missing", "other namespace": "sv-other123",
				"workspace ID": "ws-test123", "malformed": "%00..%2F..",
			},
		},
		{
			name: "state output", method: http.MethodGet, pattern: "/api/v2/state-version-outputs/%s", body: "",
			existing: "sv-test123.greeting",
			variants: map[string]string{
				"unknown version": "sv-missing.greeting", "other namespace": "sv-other123.greeting",
				"workspace ID": "ws-test123.greeting", "no separator": "sv-test123",
				"empty name": "sv-test123.", "malformed": "%00..%2F..",
			},
		},
	}

	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			denied := newResolverHandler(t, stubAuthorizer{})
			serve := func(h *tfe.Handler, id string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, workspaceRequest(t, rt.method, fmt.Sprintf(rt.pattern, id), rt.body))

				return rec
			}

			want := serve(denied, rt.existing)
			checkJSONAPIError(t, want, http.StatusNotFound)

			wantAnswer := resolverAnswerOf(want)

			for label, id := range rt.variants {
				rec := serve(denied, id)
				checkJSONAPIError(t, rec, http.StatusNotFound)

				got := resolverAnswerOf(rec)
				if got.body != wantAnswer.body || !maps.EqualFunc(got.header, wantAnswer.header, slices.Equal) {
					t.Errorf("%s %q = %d %q %v, want the answer for the existing object: %d %q %v",
						label, id, got.code, got.body, got.header, wantAnswer.code, wantAnswer.body, wantAnswer.header)
				}
			}

			// The existing object answers a caller that may read it, so the
			// denied answer above comes from the check and not from a route
			// that never serves.
			allowed := newResolverHandler(t, allowAll())
			if rec := serve(allowed, rt.existing); rec.Code == http.StatusNotFound {
				t.Fatalf("allowed caller got 404 for %q: %s", rt.existing, rec.Body.String())
			}
		})
	}
}
